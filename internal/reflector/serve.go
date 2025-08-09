package reflector

import (
	"context"
	"log/slog"
	"time"

	idle "github.com/emersion/go-imap-idle"
	"github.com/emersion/go-imap/client"
)

// Serve connects to the IMAP server and listens for new messages using the IDLE command.
// When a new message arrives, it triggers the same logic as the `check` command.
func Serve(ctx context.Context) error {
	for {
		slog.Info("Connecting to IMAP server")

		imapClient, err := connectAndLogin()
		if err != nil {
			slog.Error("Failed to connect", "error", err)
			time.Sleep(30 * time.Second)
			continue
		}

		// Check for existing unread messages before entering IDLE
		slog.Info("Checking for existing unread messages")
		messages, err := FetchMatchingMailsWithClient(imapClient)
		if err != nil {
			slog.Error("Error fetching existing messages", "error", err)
		} else {
			for _, msg := range messages {
				err := ForwardMail(imapClient, msg)
				if err != nil {
					slog.Error("Error forwarding mail", "error", err)
					continue
				}
				err = markAsSeen(imapClient, msg.UID)
				if err != nil {
					slog.Error("Error marking mail as seen", "error", err)
				}
			}
		}

		// Enter IDLE mode
		slog.Info("Entering IDLE mode to listen for new messages")
		idleClient := idle.NewClient(imapClient)
		updates := make(chan client.Update)
		imapClient.Updates = updates

		done := make(chan error, 1)
		stop := make(chan struct{})

		go func() {
			done <- idleClient.Idle(stop)
		}()

		select {
		case <-ctx.Done():
			slog.Info("Shutting down IMAP IDLE loop")
			close(stop)

			// Wait for IDLE goroutine to finish with timeout
			select {
			case <-done:
				slog.Debug("IDLE goroutine finished cleanly")
			case <-time.After(5 * time.Second):
				slog.Warn("IDLE goroutine did not finish within timeout, proceeding with logout")
			}

			_ = imapClient.Logout()
			return nil
		case err := <-done:
			if err != nil {
				slog.Error("IDLE terminated with error", "error", err)
			}
			close(stop)
			_ = imapClient.Logout()
			continue
		case update := <-updates:
			switch u := update.(type) {
			case *client.MailboxUpdate:
				slog.Info("New mail detected", "exists", u.Mailbox.Messages, "recent", u.Mailbox.Recent)

				messages, err := FetchMatchingMailsWithClient(imapClient)
				if err != nil {
					slog.Error("Error fetching new messages", "error", err)
					break
				}

				if len(messages) > 0 {
					slog.Info("Found matching messages to forward", "count", len(messages))
					for _, msg := range messages {
						slog.Info("Forwarding message", "from", msg.Envelope.From[0].Address(), "subject", msg.Envelope.Subject)
						err := ForwardMail(imapClient, msg)
						if err != nil {
							slog.Error("Error forwarding mail", "error", err)
							continue
						}
						_ = markAsSeen(imapClient, msg.UID)
					}
				} else {
					slog.Info("No matching messages found to forward")
				}
			}
		}
	}
}
