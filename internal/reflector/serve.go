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
		err = processMessages(imapClient, "initial check")
		if err != nil {
			slog.Error("Error processing messages", "context", "initial check", "error", err)
		}

		// Setup IDLE mode
		idle := setupIDLE(imapClient)

		select {
		case <-ctx.Done():
			shutdownIDLE(idle.stop, idle.done, imapClient)
			return nil
		case err := <-idle.done:
			if err != nil {
				slog.Error("IDLE terminated with error", "error", err)
			}
			close(idle.stop)
			_ = imapClient.Logout()
			continue
		case update := <-idle.updates:
			if u, ok := update.(*client.MailboxUpdate); ok {
				slog.Info("New mail detected", "exists", u.Mailbox.Messages, "recent", u.Mailbox.Recent)
				_ = processMessages(imapClient, "new mail")
			}
		}
	}
}

// idleSetup holds the channels and client needed for IDLE operations
type idleSetup struct {
	idleClient *idle.Client
	updates    chan client.Update
	done       chan error
	stop       chan struct{}
}

// setupIDLE initializes IMAP IDLE mode and returns the necessary channels and clients
func setupIDLE(imapClient *client.Client) *idleSetup {
	slog.Info("Entering IDLE mode to listen for new messages")

	idleClient := idle.NewClient(imapClient)
	updates := make(chan client.Update)
	imapClient.Updates = updates

	done := make(chan error, 1)
	stop := make(chan struct{})

	go func() {
		done <- idleClient.Idle(stop)
	}()

	return &idleSetup{
		idleClient: idleClient,
		updates:    updates,
		done:       done,
		stop:       stop,
	}
}

// processMessages fetches and forwards matching messages, with context-aware logging
func processMessages(imapClient *client.Client, context string) error {
	messages, err := FetchMatchingMailsWithClient(imapClient)
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
			slog.Info("Forwarding message", "from", msg.Envelope.From[0].Address(), "subject", msg.Envelope.Subject)
		}

		if err := ForwardMail(imapClient, msg); err != nil {
			slog.Error("Error forwarding mail", "error", err)
			continue
		}

		if err := markAsSeen(imapClient, msg.UID); err != nil {
			slog.Error("Error marking mail as seen", "error", err)
		}
	}

	return nil
}

// shutdownIDLE handles the graceful shutdown of IMAP IDLE mode with proper timeouts
func shutdownIDLE(stop chan struct{}, done chan error, imapClient *client.Client) {
	slog.Info("Shutting down IMAP IDLE loop")
	close(stop)

	// Wait for IDLE goroutine to finish with timeout
	select {
	case <-done:
		slog.Debug("IDLE goroutine finished cleanly")
	case <-time.After(5 * time.Second):
		slog.Warn("IDLE goroutine did not finish within timeout, proceeding with logout")
	}

	// Logout with timeout to prevent hanging
	logoutDone := make(chan struct{})
	go func() {
		_ = imapClient.Logout()
		close(logoutDone)
	}()

	select {
	case <-logoutDone:
		slog.Debug("IMAP logout completed successfully")
	case <-time.After(3 * time.Second):
		slog.Warn("IMAP logout timed out, forcing exit")
	}
}
