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

// saveToSent uploads the given raw message to the IMAP "Sent" folder
func saveToSent(imapClient *client.Client, msgBytes []byte) error {
	// First, ensure we're in a valid state by selecting INBOX
	_, err := imapClient.Select("INBOX", true)
	if err != nil {
		slog.Debug("Could not select INBOX before saving to Sent", "error", err)
	}

	// List available folders for debugging (only once)
	mailboxes := make(chan *imap.MailboxInfo, 10)
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

	for _, folder := range sentFolders {
		err := imapClient.Append(folder, flags, date, bytes.NewReader(msgBytes))
		if err != nil {
			slog.Debug("Failed to append to folder", "folder", folder, "error", err)
			// If this is not a "no such mailbox" error, it might be the continuation issue
			if !strings.Contains(err.Error(), "no such mailbox") && 
			   !strings.Contains(err.Error(), "does not exist") {
				// For continuation request issues, try a different approach
				if strings.Contains(err.Error(), "no continuation request received") {
					slog.Debug("Continuation request issue detected, skipping Sent folder save")
					return fmt.Errorf("failed to save to Sent folder (IMAP server issue): %w", err)
				}
			}
			continue
		}
		
		slog.Debug("Successfully saved to Sent folder", "folder", folder)
		return nil
	}

	return fmt.Errorf("failed to append to any Sent folder: %w", err)
}
