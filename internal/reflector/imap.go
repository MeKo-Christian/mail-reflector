package reflector

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"log/slog"
	"mime"
	"strings"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message"
	"github.com/spf13/viper"
)

// Attachment represents a file attachment in an email
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// MailSummary contains basic info about a matching message
type MailSummary struct {
	Envelope    *imap.Envelope
	UID         uint32
	TextBody    string
	HTMLBody    string
	Attachments []Attachment
}

// FetchMatchingMails connects to the IMAP server and returns mails matching the configured "from" filter.
func FetchMatchingMails() ([]MailSummary, error) {
	slog.Info("Starting IMAP fetch process")

	// Establish IMAP connection and select INBOX
	client, err := connectAndLogin()
	if err != nil {
		slog.Error("IMAP login failed", "error", err)
		return nil, err
	}

	defer func() {
		_ = client.Logout()

		slog.Info("Logged out from IMAP server")
	}()

	slog.Info("IMAP login successful")

	// Search, fetch and extract all matching messages
	messages, err := fetchMatchingMessages(client)
	if err != nil {
		slog.Error("Failed to fetch messages", "error", err)
		return nil, err
	}

	slog.Info("Fetched matching messages", "count", len(messages))

	return messages, nil
}

// connectAndLogin establishes a secure connection to the IMAP server,
// logs in using the configured credentials, and selects the INBOX.
// Returns an authenticated IMAP client, or an error if connection or login fails.
func connectAndLogin() (*client.Client, error) {
	// Load connection parameters from config
	server := viper.GetString("imap.server")
	port := viper.GetInt("imap.port")
	username := viper.GetString("imap.username")
	password := viper.GetString("imap.password")

	// Combine server and port into full address
	address := fmt.Sprintf("%s:%d", server, port)

	// Prepare TLS configuration to secure the connection
	tlsConfig := &tls.Config{
		ServerName: server, // ensures correct certificate validation
	}

	// Dial the IMAP server using TLS
	client, err := client.DialTLS(address, tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to IMAP server: %w", err)
	}

	// Attempt to log in with the provided credentials
	if err := client.Login(username, password); err != nil {
		_ = client.Logout() // clean up if login fails
		return nil, fmt.Errorf("failed to login: %w", err)
	}

	// Select the "INBOX" mailbox in read-only mode (false = not read-only)
	if _, err := client.Select("INBOX", false); err != nil {
		_ = client.Logout() // clean up if INBOX selection fails
		return nil, fmt.Errorf("failed to select INBOX: %w", err)
	}

	// Return the logged-in and mailbox-selected IMAP client
	return client, nil
}

// fetchMatchingMessages searches the INBOX for messages from the configured "filter.from" address,
// fetches basic message data (envelope, UID, body), parses the MIME structure, and returns a list of summaries.
func fetchMatchingMessages(client *client.Client) ([]MailSummary, error) {
	// Load the sender filter (e.g., "vorstand@example.com") from config
	filterFrom := viper.GetString("filter.from")

	// Create IMAP search criteria to match messages by "From" header
	criteria := imap.NewSearchCriteria()
	criteria.Header.Add("From", filterFrom)

	// Execute the search query on the selected mailbox (INBOX)
	uids, err := client.Search(criteria)
	if err != nil {
		return nil, fmt.Errorf("failed to search: %w", err)
	}

	// No messages matched the criteria — return empty result
	if len(uids) == 0 {
		log.Println("No matching messages found")
		return nil, nil
	}

	// Create a sequence set of UIDs to fetch
	seqset := new(imap.SeqSet)
	seqset.AddNum(uids...)

	// Prepare message channel to receive fetched messages
	messages := make(chan *imap.Message, len(uids))

	// Define which parts of the message to fetch:
	// - Envelope: contains subject, sender, recipient, date, etc.
	// - UID: unique ID per message
	// - BodySectionName{}: represents the entire message body
	section := &imap.BodySectionName{}
	items := []imap.FetchItem{
		imap.FetchEnvelope,
		imap.FetchUid,
		section.FetchItem(),
	}

	// Fetch message data from server into the channel
	if err := client.Fetch(seqset, items, messages); err != nil {
		return nil, fmt.Errorf("failed to fetch messages: %w", err)
	}

	var results []MailSummary

	// Process each fetched message
	for msg := range messages {
		// Retrieve the raw message body from the fetched section
		body := msg.GetBody(section)
		if body == nil {
			log.Println("No body found in message")
			continue
		}

		// Parse the MIME structure of the message using go-message
		entity, err := message.Read(body)
		if err != nil {
			log.Printf("Failed to parse MIME message: %v", err)
			continue
		}

		// Extract plain text, HTML body, and attachments
		text, html, attachments := extractBodies(entity)

		// Add the parsed message data to the result list
		results = append(results, MailSummary{
			Envelope:    msg.Envelope,
			UID:         msg.Uid,
			TextBody:    text,
			HTMLBody:    html,
			Attachments: attachments,
		})
	}

	return results, nil
}

// extractBodies parses a MIME message entity and extracts:
// - text and HTML body (from multipart/alternative or single-part)
// - attachments (from multipart/mixed or similar)
func extractBodies(entity *message.Entity) (string, string, []Attachment) {
	var text, html string
	var attachments []Attachment

	// Get content type of the top-level entity (e.g. multipart/mixed)
	mediaType, _, _ := entity.Header.ContentType()

	// If it's multipart (e.g. mixed or alternative), walk through its parts
	if strings.HasPrefix(mediaType, "multipart/") {
		mr := entity.MultipartReader()

		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break // done reading parts
			}

			if err != nil {
				break // skip faulty parts
			}

			// Get the content type and disposition of this part
			partMediaType, _, _ := part.Header.ContentType()
			disposition, _, _ := part.Header.ContentDisposition()

			// Read the body content
			body, _ := io.ReadAll(part.Body)

			// Handle attachments
			if disposition == "attachment" {
				filename := "attachment"

				if cd := part.Header.Get("Content-Disposition"); cd != "" {
					_, params, err := mime.ParseMediaType(cd)

					if err == nil {
						if name, ok := params["filename"]; ok {
							filename = name
						}
					}
				}

				attachments = append(attachments, Attachment{
					Filename:    filename,
					ContentType: partMediaType,
					Data:        body,
				})

				continue
			}

			// Handle inline parts (body content)
			switch partMediaType {
			case "text/plain":
				text = string(body)
			case "text/html":
				html = string(body)
			}
		}
	} else {
		// Not multipart: could be just plain text or HTML
		body, _ := io.ReadAll(entity.Body)

		switch mediaType {
		case "text/plain":
			text = string(body)
		case "text/html":
			html = string(body)
		}
	}

	return text, html, attachments
}
