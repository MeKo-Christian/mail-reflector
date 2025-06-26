package reflector

import (
	"crypto/tls"
	"fmt"
	"log"
	"log/slog"

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
func FetchMatchingMails() ([]MailSummary, *client.Client, error) {
	slog.Info("Starting IMAP fetch process")

	// Establish IMAP connection and select INBOX
	client, err := connectAndLogin()
	if err != nil {
		slog.Error("IMAP login failed", "error", err)
		return nil, nil, err
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
		return nil, nil, err
	}

	slog.Info("Fetched matching messages", "count", len(messages))

	return messages, client, nil
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
	_, err = client.Select("INBOX", false) // false = read-write
	if err != nil {
		_ = client.Logout()
		return nil, fmt.Errorf("failed to select INBOX: %w", err)
	}

	return client, nil
}

// fetchMatchingMessages searches the INBOX for messages from the configured "filter.from" address,
// fetches basic message data (envelope, UID, body), parses the MIME structure, and returns a list of summaries.
func fetchMatchingMessages(client *client.Client) ([]MailSummary, error) {
	// Load the sender filter (e.g., "vorstand@example.com") from config
	filterFroms := viper.GetStringSlice("filter.from")

	// Create IMAP search criteria to match messages by "From" header
	criteria := imap.NewSearchCriteria()

	for _, sender := range filterFroms {
		criteria.Header.Add("From", sender)
	}
	criteria.WithoutFlags = []string{imap.SeenFlag}

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

	results := make([]MailSummary, 0, len(uids))

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
