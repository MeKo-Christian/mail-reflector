package reflector

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

const (
	// mailboxesChanBufferSize defines the buffer size for the mailboxes channel
	// This should be large enough to handle most email providers' folder counts
	mailboxesChanBufferSize = 50
)

// SentFolderUnsupportedError represents an error when the IMAP server doesn't support
// saving to Sent folder due to continuation request limitations
type SentFolderUnsupportedError struct {
	Underlying error
}

func (e *SentFolderUnsupportedError) Error() string {
	return fmt.Sprintf("IMAP server doesn't support saving to Sent folder: %v", e.Underlying)
}

// IsSentFolderUnsupported checks if an error is a SentFolderUnsupportedError
func IsSentFolderUnsupported(err error) bool {
	_, ok := err.(*SentFolderUnsupportedError)
	return ok
}

// saveToSent uploads the given raw message to the IMAP "Sent" folder
func saveToSent(imapClient *client.Client, msgBytes []byte) error {
	// Note: INBOX should already be selected in read-write mode from connectAndLogin

	// List available folders for debugging (only once)
	mailboxes := make(chan *imap.MailboxInfo, mailboxesChanBufferSize)
	done := make(chan error, 1)
	go func() {
		done <- imapClient.List("", "*", mailboxes)
	}()

	var folderNames []string
	for m := range mailboxes {
		folderNames = append(folderNames, m.Name)
	}

	if err := <-done; err != nil {
		slog.Debug("Could not list folders for debugging", "error", err)
	} else {
		slog.Debug("Available IMAP folders", "folders", folderNames)
	}

	// Try common Sent folder names (including German providers like Strato)
	sentFolders := []string{
		"Sent Items",
		"Sent Messages",
		"Sent",
		"Gesendet",
		"Gesendete Elemente",
		"Gesendete Objekte",
		"INBOX.Sent",
		"INBOX.Sent Items",
		"INBOX.Gesendet",
	}

	flags := []string{imap.SeenFlag}
	date := time.Now()

	var lastErr error
	for _, folder := range sentFolders {
		err := imapClient.Append(folder, flags, date, bytes.NewReader(msgBytes))
		if err != nil {
			lastErr = err
			slog.Debug("Failed to append to folder", "folder", folder, "error", err)
			// If this is not a "no such mailbox" error, it might be the continuation issue
			if !isNoSuchMailboxError(err) {
				// For continuation request issues, suppress the warning and fail gracefully
				if isNoContinuationRequestError(err) {
					slog.Debug("Continuation request issue detected, IMAP server doesn't support this feature")
					// Return a special error that won't be logged as a warning in the caller
					return &SentFolderUnsupportedError{Underlying: err}
				}
			}
			continue
		}

		slog.Debug("Successfully saved to Sent folder", "folder", folder)
		return nil
	}

	if lastErr != nil {
		return fmt.Errorf("failed to append to any Sent folder: %w", lastErr)
	}
	return fmt.Errorf("failed to append to any Sent folder: no folders were tried")
}

// isNoSuchMailboxError checks if the error indicates a mailbox doesn't exist
func isNoSuchMailboxError(err error) bool {
	errorStr := strings.ToLower(err.Error())
	return strings.Contains(errorStr, "no such mailbox") ||
		strings.Contains(errorStr, "does not exist") ||
		strings.Contains(errorStr, "mailbox does not exist")
}

// isNoContinuationRequestError checks if the error is related to IMAP continuation request issues
func isNoContinuationRequestError(err error) bool {
	errorStr := strings.ToLower(err.Error())
	return strings.Contains(errorStr, "no continuation request received") ||
		strings.Contains(errorStr, "continuation request")
}
