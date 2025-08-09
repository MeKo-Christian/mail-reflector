package reflector

import (
	"crypto/tls"
	"fmt"
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

	// Fetch all unread messages with envelopes and bodies in one go
	// This approach avoids UID invalidation issues between separate fetches
	seqset := new(imap.SeqSet)
	seqset.AddNum(uids...)

	slog.Debug("Fetching all unread messages with full data", "uids", uids, "count", len(uids))

	// Prepare message channel to receive all fetched messages
	allMessages := make(chan *imap.Message, len(uids))

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

	// Fetch all unread message data from server into the channel
	if err := client.Fetch(seqset, items, allMessages); err != nil {
		slog.Error("IMAP fetch failed", "error", err, "uids", uids, "seqset", seqset.String())
		return nil, fmt.Errorf("failed to fetch messages: %w", err)
	}

	slog.Debug("Successfully fetched all messages, filtering and processing")

	results := make([]MailSummary, 0)
	matchingUIDs := make([]uint32, 0)
	nonMatchingUIDs := make([]uint32, 0)
	processedCount := 0

	// Process and filter messages simultaneously
	for msg := range allMessages {
		processedCount++
		slog.Debug("Processing message", "uid", msg.Uid, "processed_count", processedCount, "expected_total", len(uids))

		// Check if this message matches our filter criteria
		if !isFromAddressMatching(msg.Envelope, normalizedFilters) {
			nonMatchingUIDs = append(nonMatchingUIDs, msg.Uid)
			slog.Debug("Message does not match filter", "uid", msg.Uid, "from", getFromAddress(msg.Envelope))
			continue
		}

		// Message matches - add to matching list and process
		matchingUIDs = append(matchingUIDs, msg.Uid)
		slog.Debug("Message matches filter", "uid", msg.Uid, "from", getFromAddress(msg.Envelope))

		// Retrieve the raw message body from the fetched section
		body := msg.GetBody(section)
		if body == nil {
			slog.Warn("No body found in matching message", "uid", msg.Uid)
			continue
		}

		// Parse the MIME structure of the message using go-message
		entity, err := message.Read(body)
		if err != nil {
			slog.Error("Failed to parse MIME message", "uid", msg.Uid, "error", err)
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

		slog.Debug("Successfully processed matching message", "uid", msg.Uid)
	}

	// Log summary of matching vs non-matching messages
	if viper.GetBool("verbose") {
		logFilteringSummary(client, matchingUIDs, nonMatchingUIDs, normalizedFilters)
	}

	slog.Debug("Message processing complete", "processed", processedCount, "matching", len(matchingUIDs), "results", len(results))

	// No messages matched the criteria — return empty result
	if len(results) == 0 {
		slog.Info("No messages match the filter criteria", "unread_total", len(uids))
		return nil, nil
	}

	slog.Info("Found matching messages", "matching", len(results), "total_unread", len(uids))

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
