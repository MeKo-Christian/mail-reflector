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
	// imapValidationTimeout defines how long to wait for UID validation
	imapValidationTimeout = 10 * time.Second
	// imapSingleMessageTimeout defines how long to wait for individual message fetch
	imapSingleMessageTimeout = 15 * time.Second
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

	// Use two-phase approach to handle invalid or stale UIDs gracefully:
	// UIDs returned by search may be invalid/stale due to concurrent mailbox changes or server inconsistencies
	// Phase 1: Validate UIDs by fetching just envelopes
	messages, err := fetchMessagesRobustly(ctx, client, uids, normalizedFilters)
	if err != nil {
		return nil, err
	}

	return messages, nil
}

// fetchMessagesRobustly implements a two-phase fetch approach to handle problematic UIDs
func fetchMessagesRobustly(ctx context.Context, client *client.Client, uids []uint32, filters []string) ([]MailSummary, error) {
	slog.Debug("Starting robust message fetch", "uids", uids, "count", len(uids))

	// Phase 1: Validate all UIDs by fetching just envelopes
	validUIDs, err := validateUIDs(ctx, client, uids)
	if err != nil {
		slog.Error("UID validation failed", "error", err)
		return nil, fmt.Errorf("UID validation failed: %w", err)
	}

	if len(validUIDs) == 0 {
		slog.Info("No valid UIDs found")
		return nil, nil
	}

	slog.Debug("UID validation complete", "valid_uids", validUIDs, "valid_count", len(validUIDs), "original_count", len(uids))

	// Phase 2: Fetch message bodies one by one for valid UIDs
	results := make([]MailSummary, 0, len(validUIDs))
	matchingUIDs := make([]uint32, 0, len(validUIDs))
	nonMatchingUIDs := make([]uint32, 0, len(validUIDs))

	for _, uid := range validUIDs {
		select {
		case <-ctx.Done():
			slog.Error("Message processing cancelled", "processed", len(results), "remaining", len(validUIDs)-len(results))
			return nil, fmt.Errorf("message processing cancelled: %w", ctx.Err())
		default:
		}

		mailSummary, matches, err := fetchSingleMessage(ctx, client, uid, filters)
		if err != nil {
			slog.Warn("Failed to fetch individual message, skipping", "uid", uid, "error", err)
			continue
		}

		if matches {
			matchingUIDs = append(matchingUIDs, uid)
			results = append(results, *mailSummary)
			slog.Debug("Successfully processed matching message", "uid", uid)
		} else {
			nonMatchingUIDs = append(nonMatchingUIDs, uid)
		}
	}

	// Log summary of results
	if viper.GetBool("verbose") {
		logFilteringSummary(client, matchingUIDs, nonMatchingUIDs, filters)
	}

	slog.Debug("Robust fetch complete", "total_results", len(results), "matching", len(matchingUIDs), "non_matching", len(nonMatchingUIDs))

	return results, nil
}

// validateUIDs checks if UIDs are valid by fetching just envelope data
func validateUIDs(ctx context.Context, client *client.Client, uids []uint32) ([]uint32, error) {
	seqset := new(imap.SeqSet)
	seqset.AddNum(uids...)

	slog.Debug("Validating UIDs", "uids", uids, "seqset", seqset.String())

	// Create a shorter timeout for validation
	validationCtx, cancel := context.WithTimeout(ctx, imapValidationTimeout)
	defer cancel()

	messages := make(chan *imap.Message, len(uids))

	fetchDone := make(chan error, 1)
	go func() {
		fetchDone <- client.UidFetch(seqset, []imap.FetchItem{imap.FetchEnvelope, imap.FetchUid}, messages)
	}()

	select {
	case err := <-fetchDone:
		if err != nil {
			slog.Error("UID validation fetch failed", "error", err, "uids", uids)
			return nil, fmt.Errorf("UID validation fetch failed: %w", err)
		}
	case <-validationCtx.Done():
		slog.Error("UID validation timed out", "uids", uids)
		return nil, fmt.Errorf("UID validation timed out: %w", validationCtx.Err())
	}

	validUIDs := make([]uint32, 0, len(uids))
	for msg := range messages {
		if msg != nil && msg.Uid > 0 {
			validUIDs = append(validUIDs, msg.Uid)
		}
	}

	slog.Debug("UID validation complete", "requested", len(uids), "valid", len(validUIDs))
	return validUIDs, nil
}

// fetchSingleMessage fetches a single message with full body and checks if it matches filters
func fetchSingleMessage(ctx context.Context, client *client.Client, uid uint32, filters []string) (*MailSummary, bool, error) {
	slog.Debug("Fetching individual message", "uid", uid)

	// Create timeout for individual message fetch
	msgCtx, cancel := context.WithTimeout(ctx, imapSingleMessageTimeout)
	defer cancel()

	seqset := new(imap.SeqSet)
	seqset.AddNum(uid)

	section := &imap.BodySectionName{}
	items := []imap.FetchItem{
		imap.FetchEnvelope,
		imap.FetchUid,
		section.FetchItem(),
	}

	messages := make(chan *imap.Message, 1)
	fetchDone := make(chan error, 1)
	go func() {
		fetchDone <- client.UidFetch(seqset, items, messages)
	}()

	select {
	case err := <-fetchDone:
		if err != nil {
			return nil, false, fmt.Errorf("failed to fetch message %d: %w", uid, err)
		}
	case <-msgCtx.Done():
		return nil, false, fmt.Errorf("fetch timeout for message %d: %w", uid, msgCtx.Err())
	}

	// Get the message from channel
	msg, ok := <-messages
	if !ok || msg == nil {
		return nil, false, fmt.Errorf("no message received for UID %d", uid)
	}

	// Check if message matches filter
	matches := isFromAddressMatching(msg.Envelope, filters)
	if !matches {
		slog.Debug("Message does not match filter", "uid", uid, "from", getFromAddress(msg.Envelope))
		return nil, false, nil
	}

	// Process message body
	body := msg.GetBody(section)
	if body == nil {
		return nil, true, fmt.Errorf("no body found for message %d", uid)
	}

	entity, err := message.Read(body)
	if err != nil {
		return nil, true, fmt.Errorf("failed to parse message %d: %w", uid, err)
	}

	text, html, attachments := extractBodies(entity)

	mailSummary := &MailSummary{
		Envelope:    msg.Envelope,
		UID:         msg.Uid,
		TextBody:    text,
		HTMLBody:    html,
		Attachments: attachments,
	}

	return mailSummary, true, nil
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
	if err := client.UidFetch(seqset, []imap.FetchItem{imap.FetchEnvelope, imap.FetchUid}, messages); err != nil {
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
