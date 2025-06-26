package reflector

import (
	"fmt"
	"log/slog"
)

// CheckAndForward checks the IMAP inbox and sends mails if matching messages are found.
func CheckAndForward() error {
	fmt.Println("Connecting to IMAP...")

	mails, client, err := FetchMatchingMails()
	if err != nil {
		return err
	}

	defer client.Logout()

	if len(mails) == 0 {
		fmt.Println("No matching mails to forward.")
		return nil
	}

	for _, mail := range mails {
		slog.Info("Forwarding mail", "subject", mail.Envelope.Subject, "uid", mail.UID)

		if err := ForwardMail(client, mail); err != nil {
			slog.Error("Failed to forward", "uid", mail.UID, "error", err)
			continue
		}

		if err := markAsSeen(client, mail.UID); err != nil {
			slog.Warn("Could not mark mail as seen", "uid", mail.UID, "error", err)
		}
	}

	return nil
}
