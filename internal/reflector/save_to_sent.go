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

// saveToSent uploads the given raw message to the IMAP "Sent" folder
func saveToSent(imapClient *client.Client, msgBytes []byte) error {
	// First, ensure we're in a valid state by selecting INBOX
	_, err := imapClient.Select("INBOX", true)
	if err != nil {
		slog.Debug("Could not select INBOX before saving to Sent", "error", err)
	}

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
		"Sent",
		"Sent Items",
		"Sent Messages",
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
				// For continuation request issues, try a different approach
				if isNoContinuationRequestError(err) {
					slog.Debug("Continuation request issue detected, skipping Sent folder save")
					return fmt.Errorf("failed to save to Sent folder (IMAP server issue): %w", err)
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
