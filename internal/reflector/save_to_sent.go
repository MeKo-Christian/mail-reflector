package reflector

import (
	"bytes"
	"fmt"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

// saveToSent uploads the given raw message to the IMAP "Sent" folder
func saveToSent(imapClient *client.Client, msgBytes []byte) error {
	flags := []string{imap.SeenFlag}
	date := time.Now()

	err := imapClient.Append("Sent", flags, date, bytes.NewReader(msgBytes))
	if err != nil {
		return fmt.Errorf("failed to append to Sent folder: %w", err)
	}

	return nil
}
