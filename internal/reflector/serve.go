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
		slog.Info("Connecting to IMAP server in IDLE mode")

		imapClient, err := connectAndLogin()
		if err != nil {
			slog.Error("Failed to connect", "error", err)
			time.Sleep(30 * time.Second)
			continue
		}

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
			switch update.(type) {
			case *client.MailboxUpdate:
				slog.Info("New mail detected, running check logic")

				messages, err := FetchMatchingMailsWithClient(imapClient)
				if err != nil {
					slog.Error("Error fetching new messages", "error", err)
					break
				}

				for _, msg := range messages {
					err := ForwardMail(imapClient, msg)
					if err != nil {
						slog.Error("Error forwarding mail", "error", err)
						continue
					}
					_ = markAsSeen(imapClient, msg.UID)
				}
			}
		}
	}
}
