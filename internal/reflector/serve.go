package reflector

import (
	"context"
	"log/slog"
	"math"
	"time"

	idle "github.com/emersion/go-imap-idle"
	"github.com/emersion/go-imap/client"
)

const (
	// maxRetryAttempts defines how many times to retry failed operations
	maxRetryAttempts = 3
	// baseRetryDelay is the base delay for exponential backoff
	baseRetryDelay = 1 * time.Second
	// maxRetryDelay caps the maximum retry delay
	maxRetryDelay = 30 * time.Second
)

// Serve connects to the IMAP server and listens for new messages using the IDLE command.
// When a new message arrives, it triggers the same logic as the `check` command.
func Serve(ctx context.Context) error {
	connectionAttempt := 0

	for {
		connectionAttempt++
		slog.Info("Connecting to IMAP server", "attempt", connectionAttempt)

		imapClient, err := connectAndLogin()
		if err != nil {
			slog.Error("Failed to connect", "error", err, "attempt", connectionAttempt)

			// Use exponential backoff for connection retries
			delay := time.Duration(math.Min(float64(connectionAttempt), 6)) * 10 * time.Second
			if delay > 5*time.Minute {
				delay = 5 * time.Minute
			}

			slog.Info("Retrying connection after delay", "delay", delay, "next_attempt", connectionAttempt+1)
			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return nil
			}
		}

		// Reset connection attempt counter on successful connection
		connectionAttempt = 0

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

// processMessages fetches and forwards matching messages, with context-aware logging and retry logic
func processMessages(imapClient *client.Client, context string) error {
	// Retry message fetching with exponential backoff
	messages, err := retryOperation(func() ([]MailSummary, error) {
		return FetchMatchingMailsWithClient(imapClient)
	}, context+" fetch")
	if err != nil {
		slog.Error("Error fetching messages after retries", "context", context, "error", err)
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

// retryOperation performs an operation with exponential backoff retry logic
func retryOperation(operation func() ([]MailSummary, error), context string) ([]MailSummary, error) {
	var lastErr error

	for attempt := 1; attempt <= maxRetryAttempts; attempt++ {
		slog.Debug("Attempting operation", "context", context, "attempt", attempt, "max_attempts", maxRetryAttempts)

		result, err := operation()
		if err == nil {
			if attempt > 1 {
				slog.Info("Operation succeeded after retry", "context", context, "attempt", attempt)
			}
			return result, nil
		}

		lastErr = err
		slog.Warn("Operation failed", "context", context, "attempt", attempt, "error", err)

		// Don't retry on the last attempt
		if attempt == maxRetryAttempts {
			break
		}

		// Calculate exponential backoff delay
		delay := time.Duration(math.Pow(2, float64(attempt-1))) * baseRetryDelay
		if delay > maxRetryDelay {
			delay = maxRetryDelay
		}

		slog.Debug("Retrying after delay", "context", context, "delay", delay, "next_attempt", attempt+1)
		time.Sleep(delay)
	}

	return nil, lastErr
}
