package reflector

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message"
	"github.com/spf13/viper"
)

const (
	// imapFetchTimeout defines how long to wait for IMAP fetch operations
	imapFetchTimeout = 30 * time.Second
	// imapConnectTimeout defines how long to wait for IMAP connection
	imapConnectTimeout = 10 * time.Second
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

	// Create context with timeout for fetch operations
	ctx, cancel := context.WithTimeout(context.Background(), imapFetchTimeout)
	defer cancel()

	messages, err := fetchMatchingMessages(ctx, client)
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
	return connectAndLoginWithTimeout(imapConnectTimeout)
}

// connectAndLoginWithTimeout establishes a secure connection with specified timeout
func connectAndLoginWithTimeout(timeout time.Duration) (*client.Client, error) {
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

	// Create context with timeout for connection
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Dial the IMAP server using TLS with timeout protection
	connectionDone := make(chan *client.Client, 1)
	connectionErr := make(chan error, 1)
	go func() {
		client, err := client.DialTLS(address, tlsConfig)
		if err != nil {
			connectionErr <- err
			return
		}
		connectionDone <- client
	}()

	var imapClient *client.Client
	select {
	case imapClient = <-connectionDone:
		// Connection successful
	case err := <-connectionErr:
		return nil, fmt.Errorf("failed to connect to IMAP server: %w", err)
	case <-ctx.Done():
		return nil, fmt.Errorf("connection timed out after %v: %w", timeout, ctx.Err())
	}

	// Check connection health before proceeding
	if err := checkConnectionHealth(imapClient); err != nil {
		_ = imapClient.Logout()
		return nil, fmt.Errorf("connection health check failed: %w", err)
	}

	// Attempt to log in with the provided credentials
	if err := imapClient.Login(username, password); err != nil {
		_ = imapClient.Logout() // clean up if login fails
		return nil, fmt.Errorf("failed to login: %w", err)
	}

	// Select the "INBOX" mailbox in read-only mode (false = not read-only)
	_, err := imapClient.Select("INBOX", false) // false = read-write
	if err != nil {
		_ = imapClient.Logout()
		return nil, fmt.Errorf("failed to select INBOX: %w", err)
	}

	return imapClient, nil
}

// fetchMatchingMessages searches the INBOX for messages from the configured "filter.from" address,
// fetches basic message data (envelope, UID, body), parses the MIME structure, and returns a list of summaries.
func fetchMatchingMessages(ctx context.Context, client *client.Client) ([]MailSummary, error) {
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

	// Check if context is already cancelled
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("fetch operation cancelled: %w", ctx.Err())
	default:
	}

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

	// Fetch all unread message data from server with timeout protection
	fetchDone := make(chan error, 1)
	go func() {
		fetchDone <- client.Fetch(seqset, items, allMessages)
	}()

	// Wait for fetch to complete or timeout
	select {
	case err := <-fetchDone:
		if err != nil {
			slog.Error("IMAP fetch failed", "error", err, "uids", uids, "seqset", seqset.String())
			return nil, fmt.Errorf("failed to fetch messages: %w", err)
		}
	case <-ctx.Done():
		slog.Error("IMAP fetch timed out", "timeout", imapFetchTimeout, "uids", uids)
		return nil, fmt.Errorf("fetch operation timed out after %v: %w", imapFetchTimeout, ctx.Err())
	}

	slog.Debug("Successfully fetched all messages, filtering and processing")

	results := make([]MailSummary, 0, len(uids))
	matchingUIDs := make([]uint32, 0, len(uids))
	nonMatchingUIDs := make([]uint32, 0, len(uids))
	processedCount := 0

	// Process and filter messages simultaneously with timeout protection
	for {
		select {
		case msg, ok := <-allMessages:
			if !ok {
				// Channel closed, we're done
				goto processingComplete
			}
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
		case <-ctx.Done():
			slog.Error("Message processing timed out", "processed", processedCount, "expected", len(uids))
			return nil, fmt.Errorf("message processing timed out: %w", ctx.Err())
		}
	}

processingComplete:

	// Log summary of matching vs non-matching messages
	if viper.GetBool("verbose") {
		logFilteringSummary(client, matchingUIDs, nonMatchingUIDs, normalizedFilters)
	}

	slog.Debug("Message processing complete", "processed", processedCount, "expected", len(uids), "matching", len(matchingUIDs), "results", len(results))

	// Validate that all expected messages were processed
	if processedCount != len(uids) {
		slog.Warn("Not all expected messages were processed",
			"expected", len(uids),
			"processed", processedCount,
			"missing", len(uids)-processedCount)
	}

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

	return slices.Contains(normalizedFilters, fromAddress)
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

// checkConnectionHealth performs a basic health check on the IMAP connection
func checkConnectionHealth(client *client.Client) error {
	// Try to get server capability to ensure connection is working
	_, err := client.Capability()
	if err != nil {
		return fmt.Errorf("capability check failed: %w", err)
	}
	return nil
}
