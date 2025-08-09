package reflector

import (
	"crypto/tls"
	"fmt"
	"log"
	"log/slog"
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
func FetchMatchingMails() ([]MailSummary, *client.Client, error) {
	client, err := connectAndLogin()
	if err != nil {
		slog.Error("IMAP login failed", "error", err)
		return nil, nil, err
	}

	defer func() {
		_ = client.Logout()

		slog.Info("Logged out from IMAP server")
	}()

	mailSummary, err := FetchMatchingMailsWithClient(client)

	return mailSummary, client, err
}

// FetchMatchingMailsWithClient uses an existing IMAP client to fetch mails matching the configured "from" filter.
func FetchMatchingMailsWithClient(client *client.Client) ([]MailSummary, error) {
	slog.Info("Searching for matching mails")

	messages, err := fetchMatchingMessages(client)
	if err != nil {
		slog.Error("Failed to fetch matching messages", "error", err)
		return nil, err
	}

	slog.Info("Fetched messages", "count", len(messages))

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

	// Normalize filter emails to lowercase for case-insensitive matching
	normalizedFilters := make([]string, len(filterFroms))
	for i, email := range filterFroms {
		normalizedFilters[i] = strings.ToLower(email)
	}

	slog.Debug("Email filter configuration", "original_emails", filterFroms, "normalized_emails", normalizedFilters)

	// Search for all unread messages first, then filter by From address
	// This approach is more reliable than using multiple IMAP header criteria
	criteria := imap.NewSearchCriteria()
	criteria.WithoutFlags = []string{imap.SeenFlag}

	// Execute the search query on the selected mailbox (INBOX)
	uids, err := client.Search(criteria)
	if err != nil {
		return nil, fmt.Errorf("failed to search: %w", err)
	}

	// No unread messages found
	if len(uids) == 0 {
		slog.Info("No unread messages found")
		return nil, nil
	}

	slog.Debug("Found unread messages", "count", len(uids))

	// Create a sequence set of UIDs to fetch envelopes first for filtering
	seqset := new(imap.SeqSet)
	seqset.AddNum(uids...)

	// First, fetch just envelopes to filter by From address
	envelopeMessages := make(chan *imap.Message, len(uids))
	if err := client.Fetch(seqset, []imap.FetchItem{imap.FetchEnvelope, imap.FetchUid}, envelopeMessages); err != nil {
		return nil, fmt.Errorf("failed to fetch envelopes: %w", err)
	}

	// Filter messages by From address
	matchingUIDs := make([]uint32, 0)
	nonMatchingUIDs := make([]uint32, 0)

	for msg := range envelopeMessages {
		if isFromAddressMatching(msg.Envelope, normalizedFilters) {
			matchingUIDs = append(matchingUIDs, msg.Uid)
			slog.Debug("Message matches filter", "uid", msg.Uid, "from", getFromAddress(msg.Envelope))
		} else {
			nonMatchingUIDs = append(nonMatchingUIDs, msg.Uid)
		}
	}

	// Log summary of matching vs non-matching messages
	if viper.GetBool("verbose") {
		logFilteringSummary(client, matchingUIDs, nonMatchingUIDs, normalizedFilters)
	}

	// No messages matched the criteria — return empty result
	if len(matchingUIDs) == 0 {
		slog.Info("No messages match the filter criteria", "unread_total", len(uids))
		return nil, nil
	}

	slog.Info("Found matching messages", "matching", len(matchingUIDs), "total_unread", len(uids))

	// Now fetch the full message data for matching messages
	matchingSeqset := new(imap.SeqSet)
	matchingSeqset.AddNum(matchingUIDs...)

	// Prepare message channel to receive fetched messages
	messages := make(chan *imap.Message, len(matchingUIDs))

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
	if err := client.Fetch(matchingSeqset, items, messages); err != nil {
		return nil, fmt.Errorf("failed to fetch messages: %w", err)
	}

	results := make([]MailSummary, 0, len(matchingUIDs))

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

// logNonMatchingMessages logs details about non-matching messages for debugging
func logNonMatchingMessages(client *client.Client, nonMatchingUIDs []uint32) {
	if len(nonMatchingUIDs) == 0 {
		return
	}

	// Fetch envelope info for non-matching messages
	seqset := new(imap.SeqSet)
	seqset.AddNum(nonMatchingUIDs...)

	messages := make(chan *imap.Message, len(nonMatchingUIDs))
	if err := client.Fetch(seqset, []imap.FetchItem{imap.FetchEnvelope, imap.FetchUid}, messages); err != nil {
		slog.Debug("Failed to fetch non-matching message envelopes", "error", err)
		return
	}

	slog.Debug("Non-matching unread messages:")
	for msg := range messages {
		sender := "unknown"
		rawFromFormat := "unknown"
		subject := "no subject"
		if msg.Envelope != nil {
			if len(msg.Envelope.From) > 0 && msg.Envelope.From[0] != nil {
				addr := msg.Envelope.From[0]
				sender = addr.Address()
				// Show the raw From header format for debugging
				if addr.PersonalName != "" {
					rawFromFormat = fmt.Sprintf("%s <%s>", addr.PersonalName, addr.Address())
				} else {
					rawFromFormat = addr.Address()
				}
			}
			if msg.Envelope.Subject != "" {
				subject = msg.Envelope.Subject
			}
		}
		slog.Debug("Non-matching message", "uid", msg.Uid, "from_address", sender, "raw_from_format", rawFromFormat, "subject", subject)
	}
}

// isFromAddressMatching checks if the message's From address matches any of the filter criteria
func isFromAddressMatching(envelope *imap.Envelope, normalizedFilters []string) bool {
	if envelope == nil || len(envelope.From) == 0 || envelope.From[0] == nil {
		return false
	}

	fromAddress := strings.ToLower(envelope.From[0].Address())

	for _, filter := range normalizedFilters {
		if fromAddress == filter {
			return true
		}
	}

	return false
}

// getFromAddress safely extracts the From address from an envelope
func getFromAddress(envelope *imap.Envelope) string {
	if envelope == nil || len(envelope.From) == 0 || envelope.From[0] == nil {
		return "unknown"
	}
	return envelope.From[0].Address()
}

// logFilteringSummary logs detailed information about message filtering results
func logFilteringSummary(client *client.Client, matchingUIDs, nonMatchingUIDs []uint32, filters []string) {
	totalUnread := len(matchingUIDs) + len(nonMatchingUIDs)

	slog.Info("Message filtering summary",
		"total_unread", totalUnread,
		"matching_filter", len(matchingUIDs),
		"not_matching_filter", len(nonMatchingUIDs),
		"active_filters", filters)

	// Log details about non-matching messages if there are any (limit to avoid spam)
	if len(nonMatchingUIDs) > 0 && len(nonMatchingUIDs) <= 10 {
		logNonMatchingMessages(client, nonMatchingUIDs)
	}
}
