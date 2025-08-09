package reflector

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"

	"github.com/emersion/go-imap/client"
	"github.com/spf13/viper"
	gomail "gopkg.in/gomail.v2"
)

// ForwardMail sends a new mail based on a matching input message.
// It preserves subject, sender info, both plain text and HTML bodies, and includes all attachments.
func ForwardMail(client *client.Client, original MailSummary) error {
	// Load SMTP config and recipient list from config
	smtpServer := viper.GetString("smtp.server")
	smtpPort := viper.GetInt("smtp.port")
	smtpUser := viper.GetString("smtp.username")
	smtpPass := viper.GetString("smtp.password")

	recipients := viper.GetStringSlice("recipients")
	subjectPrefix := viper.GetString("subject.prefix")

	// Set From to the SMTP identity, and To to the original sender
	from := smtpUser
	to := original.Envelope.From[0].Address()
	reply := original.Envelope.From[0].Address()
	var subject string
	if subjectPrefix != "" {
		subject = fmt.Sprintf("%s %s", subjectPrefix, original.Envelope.Subject)
	} else {
		subject = original.Envelope.Subject
	}

	// Compose the outgoing message
	msg := gomail.NewMessage()
	msg.SetHeader("From", from)
	msg.SetHeader("To", to)
	msg.SetHeader("Reply-To", reply)
	msg.SetHeader("Bcc", recipients...)
	msg.SetHeader("Subject", subject)

	// Set body (text/plain is required, HTML is optional and added as alternative)
	msg.SetBody("text/plain", original.TextBody)

	if original.HTMLBody != "" {
		msg.AddAlternative("text/html", original.HTMLBody)
	}

	// Attach each file from the original mail
	for _, att := range original.Attachments {
		msg.Attach(att.Filename,
			// Explicitly set Content-Type to preserve original metadata
			gomail.SetHeader(map[string][]string{
				"Content-Type": {att.ContentType},
			}),

			// Copy the raw data into the attachment
			gomail.SetCopyFunc(func(w io.Writer) error {
				_, err := w.Write(att.Data)
				return err
			}),
		)
	}

	// Configure the SMTP dialer
	dialer := gomail.NewDialer(smtpServer, smtpPort, smtpUser, smtpPass)

	// Enable secure transport if configured
	if viper.GetString("smtp.security") == "ssl" {
		dialer.SSL = true
	} else {
		// Fallback for TLS (STARTTLS): optionally skip cert verification
		dialer.TLSConfig = &tls.Config{InsecureSkipVerify: true}
	}

	// Attempt to send the message
	if err := dialer.DialAndSend(msg); err != nil {
		slog.Error("Failed to send mail", "error", err, "subject", subject, "to", recipients)
		return fmt.Errorf("failed to send mail: %w", err)
	}

	// Save to "Sent" via IMAP
	if client != nil {
		var buf bytes.Buffer
		if _, err := msg.WriteTo(&buf); err != nil {
			slog.Error("Failed to serialize message", "error", err)
			return fmt.Errorf("failed to serialize message: %w", err)
		}

		if err := saveToSent(client, buf.Bytes()); err != nil {
			// Handle known server limitation gracefully without spamming warnings
			if IsSentFolderUnsupported(err) {
				slog.Debug("Could not save to Sent folder - server doesn't support this feature", "reason", "continuation_request_unsupported")
			} else {
				slog.Warn("Could not save to Sent folder", "error", err)
			}
		} else {
			slog.Info("Saved mail to Sent folder")
		}
	}

	fmt.Printf("Forwarded mail: %s\n", subject)
	slog.Info("Forwarded mail", "subject", subject, "recipients", recipients, "recipient_count", len(recipients))

	return nil
}
