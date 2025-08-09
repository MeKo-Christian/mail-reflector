package reflector

import (
	"context"
	"log/slog"
	"time"

	"github.com/emersion/go-imap/client"
	"github.com/spf13/viper"
)

// Serve connects to the IMAP server and listens for new messages using the IDLE command.
// When a new message arrives, it triggers the same logic as the `check` command.
func Serve(ctx context.Context) error {
	connectionAttempt := 0

	for {
		// Check for cancellation at the start of each connection attempt
		select {
		case <-ctx.Done():
			slog.Info("Serve operation cancelled")
			return nil
		default:
		}

		connectionAttempt++
		slog.Info("Connecting to IMAP server", "attempt", connectionAttempt)

		rawClient, err := connectAndLogin()
		if err != nil {
			slog.Error("Failed to connect", "error", err, "attempt", connectionAttempt)

			// Use exponential backoff for connection retries
			attempts := connectionAttempt
			if attempts > 6 {
				attempts = 6
			}
			delay := time.Duration(attempts) * 10 * time.Second
			if delay > 5*time.Minute {
				delay = 5 * time.Minute
			}

			slog.Info("Retrying connection after delay", "delay", delay, "next_attempt", connectionAttempt+1)
			time.Sleep(delay)
			continue
		}

		// Create managed IMAP connection wrapper
		imapConn := newImapConn(rawClient)

		// Reset connection attempt counter on successful connection
		connectionAttempt = 0

		// Check for existing unread messages before entering IDLE
		slog.Info("Checking for existing unread messages")
		err = processMessagesWithConn(imapConn, "initial check")
		if err != nil {
			slog.Error("Error processing messages", "context", "initial check", "error", err)
		}

		// Setup IDLE mode with proper updates channel (buffered to prevent deadlock)
		updates := make(chan client.Update, 64) // buffer to allow IDLE goroutine to send final updates
		imapConn.c.Updates = updates

		// Start IDLE
		err = imapConn.startIdle()
		if err != nil {
			slog.Error("Failed to start IDLE", "error", err)
			_ = imapConn.close()
			continue
		}

		// Monitor for updates, cancellation, or errors
		// Use a single-flight worker to serialize processing and keep updates reader responsive
		work := make(chan struct{}, 1)

		for {
			select {
			case <-ctx.Done():
				slog.Info("Serve operation cancelled, shutting down IDLE")
				_ = imapConn.close()
				return nil
			case update := <-updates:
				if u, ok := update.(*client.MailboxUpdate); ok {
					slog.Info("New mail detected", "exists", u.Mailbox.Messages, "recent", u.Mailbox.Recent)

					// Dispatch processing to background goroutine to keep updates reader responsive
					select {
					case work <- struct{}{}: // only process if not already processing
						go func() {
							defer func() { <-work }() // release work token when done

							if err := processMessagesWithConn(imapConn, "new mail"); err != nil {
								slog.Error("Error processing new messages", "error", err)
							}

							// Restart IDLE after processing messages
							if err := imapConn.startIdle(); err != nil {
								slog.Error("Failed to restart IDLE after processing", "error", err)
								// Note: In goroutine, can't use goto reconnect directly
								// The connection will be handled by the next update or timeout
							}
						}()
					default:
						// Already processing; skip this update to avoid overwhelming the system
						slog.Debug("Skipping duplicate mail update - processing already in progress")
					}
				}
			}
		}
	}
}

// processMessagesWithConn fetches and forwards matching messages using imapConn wrapper
func processMessagesWithConn(imapConn *imapConn, context string) error {
	slog.Debug("Processing messages started", "context", context)

	var messages []MailSummary
	var err error

	// Use the new wrapper-aware function that properly manages IDLE state
	messages, err = FetchMatchingMailsWithConn(imapConn)
	if err != nil {
		slog.Error("Error fetching messages", "context", context, "error", err)
		return err
	}

	if len(messages) == 0 {
		slog.Info("No matching messages found", "context", context)
		return nil
	}

	slog.Info("Found matching messages to forward", "context", context, "count", len(messages))

	for _, msg := range messages {
		if len(msg.Envelope.From) > 0 {
			recipients := viper.GetStringSlice("recipients")
			slog.Info("Forwarding message", "from", msg.Envelope.From[0].Address(), "subject", msg.Envelope.Subject, "recipients", recipients, "recipient_count", len(recipients))
		}

		// Forward and mark as seen using withConn to manage IDLE state
		err = imapConn.withConn(func(c *client.Client) error {
			if err := ForwardMail(c, msg); err != nil {
				slog.Error("Error forwarding mail", "error", err)
				return err
			}

			if err := markAsSeen(c, msg.UID); err != nil {
				slog.Error("Error marking mail as seen", "error", err)
				return err
			}

			return nil
		})
		if err != nil {
			slog.Error("Error processing message", "uid", msg.UID, "error", err)
			continue
		}
	}

	return nil
}
