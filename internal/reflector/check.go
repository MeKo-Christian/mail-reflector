package reflector

import (
	"fmt"
)

// CheckAndForward checks the IMAP inbox and sends mails if matching messages are found.
func CheckAndForward() error {
	fmt.Println("Connecting to IMAP...")

	mails, err := FetchMatchingMails()
	if err != nil {
		return err
	}

	if len(mails) == 0 {
		fmt.Println("No matching mails to forward.")
		return nil
	}

	for _, mail := range mails {
		fmt.Printf("Found mail: %s from %s\n", mail.Envelope.Subject,
			mail.Envelope.From[0].Address())

		if err := ForwardMail(mail); err != nil {
			fmt.Printf("Failed to forward: %v\n", err)
		}
	}

	return nil
}
