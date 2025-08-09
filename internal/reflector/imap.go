package reflector

import (
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap-idle"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message"
	"github.com/spf13/viper"
)

// currentMailboxStatus stores the current mailbox status from the connection
var (
	currentMailboxStatus *imap.MailboxStatus
	statusMu             sync.RWMutex
)

// problematicUIDs tracks UIDs that have failed multiple times to avoid repeated attempts
var (
	problematicUIDs = make(map[uint32]int) // UID -> failure count
	probMu          sync.Mutex
)

// Default timeout for IMAP operations
const defaultIMAPTimeout = 30 * time.Second

// Maximum failures before marking a UID as problematic
const maxFailuresBeforeSkip = 3

// imapConn wraps an IMAP client connection and manages IDLE state safely
type imapConn struct {
	c           *client.Client
	mu          sync.Mutex
	idling      bool
	idleStop    chan struct{}
	idleWG      sync.WaitGroup
	idler       *idle.Client
	currentMbox string // track current selected mailbox
}

// newImapConn creates a new IMAP connection wrapper
func newImapConn(client *client.Client) *imapConn {
	return &imapConn{
		c: client,
	}
}

// startIdle begins IDLE if not already idling
func (ic *imapConn) startIdle() error {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	if ic.idling {
		slog.Debug("IDLE already active, skipping")
		return nil
	}

	slog.Debug("Starting IMAP IDLE")
	ic.idler = idle.NewClient(ic.c)
	ic.idleStop = make(chan struct{})
	ic.idling = true
	ic.idleWG.Add(1)

	go func() {
		defer ic.idleWG.Done()
		// IdleWithFallback returns when stop is closed or server doesn't support IDLE
		// 0 timeout means no automatic timeout - IDLE continues until stop channel is closed
		err := ic.idler.IdleWithFallback(ic.idleStop, 0)
		if err != nil {
			slog.Debug("IDLE finished with error", "error", err)
		} else {
			slog.Debug("IDLE finished normally")
		}
	}()

	return nil
}

// stopIdle stops IDLE and waits for it to end
func (ic *imapConn) stopIdle() {
	ic.mu.Lock()
	if !ic.idling {
		ic.mu.Unlock()
		slog.Debug("IDLE not active, nothing to stop")
		return
	}

	slog.Debug("Stopping IMAP IDLE")
	close(ic.idleStop)
	ic.idling = false
	ic.mu.Unlock()

	// Wait for IDLE goroutine to finish with timeout
	done := make(chan struct{})
	go func() {
		ic.idleWG.Wait()
		close(done)
	}()

	select {
	case <-done:
		slog.Debug("IDLE stopped successfully")
	case <-time.After(5 * time.Second):
		slog.Warn("Timed out waiting for IDLE to stop; proceeding anyway")
	}
}

// withConn executes a function with the IMAP client, ensuring IDLE is stopped first
func (ic *imapConn) withConn(fn func(*client.Client) error) error {
	ic.stopIdle()
	ic.mu.Lock()
	defer ic.mu.Unlock()
	return fn(ic.c)
}

// selectMailbox selects a mailbox if not already selected, tracking state
func (ic *imapConn) selectMailbox(mailbox string, readOnly bool) (*imap.MailboxStatus, error) {
	// Skip redundant SELECT if already in the right mailbox
	if ic.currentMbox == mailbox {
		slog.Debug("Mailbox already selected, skipping SELECT", "mailbox", mailbox)
		return getCurrentMailboxStatus(), nil
	}

	var status *imap.MailboxStatus
	err := ic.withConn(func(c *client.Client) error {
		var err error
		status, err = c.Select(mailbox, readOnly)
		if err != nil {
			return err
		}

		// Update tracking
		ic.currentMbox = mailbox
		setCurrentMailboxStatus(status)

		slog.Debug("Selected mailbox", "mailbox", mailbox, "messages", status.Messages, "unseen", status.Unseen)
		return nil
	})

	return status, err
}

// close properly closes the connection and stops IDLE
func (ic *imapConn) close() error {
	ic.stopIdle()
	ic.mu.Lock()
	defer ic.mu.Unlock()
	return ic.c.Logout()
}

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

// uidSearchWithTimeout performs an IMAP UID search operation with a timeout
func uidSearchWithTimeout(client *client.Client, criteria *imap.SearchCriteria, timeout time.Duration) ([]uint32, error) {
	type searchResult struct {
		uids []uint32
		err  error
	}

	resultCh := make(chan searchResult, 1)

	go func() {
		uids, err := client.UidSearch(criteria)
		resultCh <- searchResult{uids: uids, err: err}
	}()

	select {
	case result := <-resultCh:
		return result.uids, result.err
	case <-time.After(timeout):
		slog.Warn("IMAP UID search operation timed out", "timeout", timeout)
		return nil, fmt.Errorf("IMAP UID search timed out after %v", timeout)
	}
}

// isProblematicUID checks if a UID has failed too many times and should be skipped
func isProblematicUID(uid uint32) bool {
	probMu.Lock()
	defer probMu.Unlock()
	count, exists := problematicUIDs[uid]
	return exists && count >= maxFailuresBeforeSkip
}

// recordUIDFailure increments the failure count for a UID
func recordUIDFailure(uid uint32) {
	probMu.Lock()
	defer probMu.Unlock()
	problematicUIDs[uid]++
	count := problematicUIDs[uid]

	if count >= maxFailuresBeforeSkip {
		slog.Warn("Marking UID as problematic after repeated failures", "uid", uid, "failure_count", count)
	} else {
		slog.Debug("Recording failure for UID", "uid", uid, "failure_count", count)
	}
}

// clearProblematicUID removes a UID from the problematic list (if it succeeds later)
func clearProblematicUID(uid uint32) {
	probMu.Lock()
	defer probMu.Unlock()
	if _, exists := problematicUIDs[uid]; exists {
		delete(problematicUIDs, uid)
		slog.Debug("Cleared UID from problematic list after successful fetch", "uid", uid)
	}
}

// setCurrentMailboxStatus sets the current mailbox status thread-safely
func setCurrentMailboxStatus(status *imap.MailboxStatus) {
	statusMu.Lock()
	defer statusMu.Unlock()
	currentMailboxStatus = status
}

// getCurrentMailboxStatus gets the current mailbox status thread-safely
func getCurrentMailboxStatus() *imap.MailboxStatus {
	statusMu.RLock()
	defer statusMu.RUnlock()
	return currentMailboxStatus
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

// FetchMatchingMailsWithConn uses the imapConn wrapper to fetch mails matching the configured "from" filter.
func FetchMatchingMailsWithConn(imapConn *imapConn) ([]MailSummary, error) {
	slog.Info("Searching for matching mails")

	// No withConn here — avoid nested locking
	messages, err := fetchMatchingMessagesWithConn(imapConn)
	if err != nil {
		slog.Error("Failed to fetch matching messages", "error", err)
		return nil, err
	}

	slog.Info("Fetched messages", "count", len(messages))

	return messages, nil
}

// fetchMatchingMessagesWithConn searches for messages using imapConn wrapper for proper IDLE management
func fetchMatchingMessagesWithConn(imapConn *imapConn) ([]MailSummary, error) {
	// Load the sender filter (e.g., "vorstand@example.com") from config
	filterFroms := viper.GetStringSlice("filter.from")

	// Normalize filter emails to lowercase for case-insensitive matching
	normalizedFilters := make([]string, len(filterFroms))
	for i, email := range filterFroms {
		normalizedFilters[i] = strings.ToLower(email)
	}

	slog.Debug("Email filter configuration", "original_emails", filterFroms, "normalized_emails", normalizedFilters)

	// Use selectMailbox to properly manage INBOX selection with tracking
	_, err := imapConn.selectMailbox("INBOX", false) // false = read-write
	if err != nil {
		return nil, fmt.Errorf("failed to select INBOX: %w", err)
	}

	// Search for all unread messages (messages without \Seen flag)
	slog.Debug("Creating search criteria")
	criteria := imap.NewSearchCriteria()
	criteria.WithoutFlags = []string{imap.SeenFlag}

	slog.Debug("Starting UID search")

	var uids []uint32
	err = imapConn.withConn(func(client *client.Client) error {
		var err error
		uids, err = uidSearchWithTimeout(client, criteria, defaultIMAPTimeout)
		return err
	})
	if err != nil {
		slog.Error("UID search failed", "error", err)
		return nil, fmt.Errorf("failed to search: %w", err)
	}

	slog.Debug("UID search completed successfully", "uids", uids, "count", len(uids))

	// No unread messages found
	if len(uids) == 0 {
		slog.Info("No unread messages found")
		return nil, nil
	}

	slog.Debug("Found unread messages", "count", len(uids))

	// Use robust approach to handle invalid or stale UIDs
	var messages []MailSummary
	err = imapConn.withConn(func(client *client.Client) error {
		var err error
		messages, err = fetchMessagesRobustly(client, uids, normalizedFilters)
		return err
	})
	if err != nil {
		return nil, err
	}

	return messages, nil
}

// connectAndLogin establishes a secure connection to the IMAP server with connection-level timeouts,
// logs in using the configured credentials, and selects the INBOX.
// Returns an authenticated IMAP client, or an error if connection or login fails.
func connectAndLogin() (*client.Client, error) {
	// Load connection parameters from config
	server := viper.GetString("imap.server")
	port := viper.GetInt("imap.port")
	username := viper.GetString("imap.username")
	password := viper.GetString("imap.password")

	// Combine server and port into full address (IPv6 compatible)
	address := net.JoinHostPort(server, fmt.Sprintf("%d", port))

	slog.Debug("Connecting to IMAP server with connection-level timeouts", "address", address)

	// Create a dialer with connection timeout
	dialer := &net.Dialer{
		Timeout: 30 * time.Second, // Connection timeout
	}

	// Establish the TCP connection with timeout
	conn, err := dialer.Dial("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to IMAP server: %w", err)
	}

	// Set read/write timeouts on the connection to prevent hanging
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("connection is not a TCP connection")
	}
	if err := tcpConn.SetReadBuffer(64 * 1024); err != nil { // 64KB buffer
		slog.Debug("Failed to set read buffer", "error", err)
	}
	if err := tcpConn.SetWriteBuffer(64 * 1024); err != nil { // 64KB buffer
		slog.Debug("Failed to set write buffer", "error", err)
	}

	// Set deadline for TLS handshake to prevent hanging
	deadline := time.Now().Add(30 * time.Second)
	_ = conn.SetDeadline(deadline)

	// Wrap connection with TLS
	tlsConfig := &tls.Config{
		ServerName: server, // ensures correct certificate validation
	}

	tlsConn := tls.Client(conn, tlsConfig)

	// Perform TLS handshake with timeout protection
	if err := tlsConn.Handshake(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("TLS handshake failed: %w", err)
	}
	// Clear deadline after successful handshake
	_ = tlsConn.SetDeadline(time.Time{})

	// Create IMAP client from the TLS connection
	imapClient, err := client.New(tlsConn)
	if err != nil {
		_ = tlsConn.Close()
		return nil, fmt.Errorf("failed to create IMAP client: %w", err)
	}

	slog.Debug("IMAP client created, setting connection timeouts")

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

	// Select the "INBOX" mailbox in read-write mode (false = not read-only)
	mailboxStatus, err := imapClient.Select("INBOX", false) // false = read-write
	if err != nil {
		_ = imapClient.Logout()
		return nil, fmt.Errorf("failed to select INBOX: %w", err)
	}

	slog.Debug("Connected to IMAP and selected INBOX in read-write mode",
		"messages", mailboxStatus.Messages,
		"recent", mailboxStatus.Recent,
		"unseen", mailboxStatus.Unseen)

	// Store mailbox status for use in search
	setCurrentMailboxStatus(mailboxStatus)

	return imapClient, nil
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

	slog.Debug("Skipping mailbox status refresh to avoid potential hanging")
	// Note: Status refresh is optional and can cause hanging issues
	// We'll rely on the cached status from connection time
	// Log current mailbox status
	if status := getCurrentMailboxStatus(); status != nil {
		slog.Debug("Current mailbox status", "unseen_count", status.Unseen, "total_messages", status.Messages)
	}

	// Search for all unread messages (messages without \Seen flag)
	slog.Debug("Creating search criteria")
	criteria := imap.NewSearchCriteria()
	criteria.WithoutFlags = []string{imap.SeenFlag}

	slog.Debug("Search criteria created", "criteria", criteria)

	// Ensure INBOX is properly selected (especially important after IDLE operations)
	slog.Debug("Ensuring INBOX is selected before search")

	// Use timeout for Select operation to prevent hanging
	selectResult := make(chan *imap.MailboxStatus, 1)
	selectErr := make(chan error, 1)

	go func() {
		status, err := client.Select("INBOX", false) // false = read-write
		if err != nil {
			selectErr <- err
		} else {
			selectResult <- status
		}
	}()

	var mailboxStatus *imap.MailboxStatus
	select {
	case mailboxStatus = <-selectResult:
		slog.Debug("INBOX selected successfully", "messages", mailboxStatus.Messages, "unseen", mailboxStatus.Unseen)
	case err := <-selectErr:
		slog.Error("Failed to select INBOX before search", "error", err)
		return nil, fmt.Errorf("failed to select INBOX: %w", err)
	case <-time.After(10 * time.Second):
		slog.Error("INBOX select operation timed out")
		return nil, fmt.Errorf("INBOX select timed out after 10s")
	}

	// Update cached status
	setCurrentMailboxStatus(mailboxStatus)
	slog.Debug("About to start UID search")

	slog.Debug("Starting UID search")
	// Execute the UID search query on the selected mailbox (INBOX) with timeout
	uids, err := uidSearchWithTimeout(client, criteria, defaultIMAPTimeout)
	if err != nil {
		slog.Error("UID search failed", "error", err)
		return nil, fmt.Errorf("failed to search: %w", err)
	}

	slog.Debug("UID search completed successfully")
	slog.Debug("UID search results for unread messages", "uids", uids, "count", len(uids))

	// Debug: Log individual UIDs to be extra clear
	for i, uid := range uids {
		slog.Debug("Found unread UID", "index", i, "uid", uid)
	}

	// Validate search results against mailbox status
	if status := getCurrentMailboxStatus(); status != nil && len(uids) > 0 && status.Unseen == 0 {
		slog.Warn("Search/status inconsistency detected",
			"search_found", len(uids),
			"mailbox_unseen", status.Unseen,
			"found_uids", uids)
		slog.Info("This may indicate stale search results or IMAP server inconsistency")
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
	slog.Debug("Starting robust message fetch", "uid_count", len(uids))
	messages, err := fetchMessagesRobustly(client, uids, normalizedFilters)
	if err != nil {
		return nil, err
	}

	return messages, nil
}

// fetchMessagesRobustly implements a two-phase fetch approach to handle problematic UIDs
func fetchMessagesRobustly(client *client.Client, uids []uint32, filters []string) ([]MailSummary, error) {
	slog.Debug("Entered fetchMessagesRobustly", "uids", uids, "count", len(uids))

	if len(uids) == 0 {
		slog.Debug("No UIDs to process, returning empty results")
		return nil, nil
	}

	// Phase 1: Validate all UIDs by fetching just envelopes
	slog.Debug("Starting UID validation phase")
	validUIDs, err := validateUIDs(client, uids)
	if err != nil {
		slog.Error("UID validation failed", "error", err)
		return nil, fmt.Errorf("UID validation failed: %w", err)
	}
	slog.Debug("UID validation completed", "valid_count", len(validUIDs))

	if len(validUIDs) == 0 {
		slog.Info("No valid UIDs found")
		return nil, nil
	}

	slog.Debug("UID validation complete", "valid_uids", validUIDs, "valid_count", len(validUIDs), "original_count", len(uids))

	// Phase 2: Fetch message bodies one by one for valid UIDs with improved error handling
	results := make([]MailSummary, 0, len(validUIDs))
	matchingUIDs := make([]uint32, 0, len(validUIDs))
	nonMatchingUIDs := make([]uint32, 0, len(validUIDs))
	failedUIDs := make([]uint32, 0) // Track failed message fetches

	skippedUIDs := make([]uint32, 0) // Track UIDs skipped due to being problematic

	for _, uid := range validUIDs {
		// Skip UIDs that have failed too many times
		if isProblematicUID(uid) {
			skippedUIDs = append(skippedUIDs, uid)
			slog.Info("Skipping problematic UID that has failed repeatedly", "uid", uid)
			continue
		}

		// Fetch individual message
		mailSummary, matches, err := fetchSingleMessage(client, uid, filters)
		if err != nil {
			failedUIDs = append(failedUIDs, uid)
			recordUIDFailure(uid) // Track the failure

			if strings.Contains(err.Error(), "timed out") {
				slog.Warn("Message fetch timed out, skipping problematic message", "uid", uid, "error", err)
			} else {
				slog.Warn("Failed to fetch individual message, skipping", "uid", uid, "error", err)
			}
			continue
		}

		// Clear from problematic list if it succeeded
		clearProblematicUID(uid)

		if matches {
			matchingUIDs = append(matchingUIDs, uid)
			results = append(results, *mailSummary)
			slog.Debug("Successfully processed matching message", "uid", uid)
		} else {
			nonMatchingUIDs = append(nonMatchingUIDs, uid)
		}
	}

	// Log comprehensive summary of results
	if viper.GetBool("verbose") {
		logFilteringSummary(client, matchingUIDs, nonMatchingUIDs, filters)
	}

	// Log processing statistics
	totalProcessed := len(matchingUIDs) + len(nonMatchingUIDs) + len(failedUIDs) + len(skippedUIDs)
	slog.Info("Message processing summary",
		"total_found", len(validUIDs),
		"total_processed", totalProcessed,
		"matching_forwarded", len(matchingUIDs),
		"non_matching", len(nonMatchingUIDs),
		"failed_fetch", len(failedUIDs),
		"skipped_problematic", len(skippedUIDs))

	if len(failedUIDs) > 0 {
		slog.Warn("Some messages could not be processed", "failed_uids", failedUIDs, "count", len(failedUIDs))
	}

	if len(skippedUIDs) > 0 {
		slog.Info("Skipped problematic messages that have failed repeatedly", "skipped_uids", skippedUIDs, "count", len(skippedUIDs))
	}

	slog.Debug("Robust fetch complete", "total_results", len(results), "matching", len(matchingUIDs), "non_matching", len(nonMatchingUIDs), "failed", len(failedUIDs))

	return results, nil
}

// validateUIDs checks if UIDs are valid by fetching just envelope data
func validateUIDs(client *client.Client, uids []uint32) ([]uint32, error) {
	slog.Debug("Entered validateUIDs", "uid_count", len(uids))

	seqset := new(imap.SeqSet)
	seqset.AddNum(uids...)

	slog.Debug("Starting UID validation fetch", "uids", uids, "seqset", seqset.String())

	messages := make(chan *imap.Message, len(uids))
	errCh := make(chan error, 1)

	// Start fetch in goroutine to avoid deadlock
	go func() {
		errCh <- client.UidFetch(seqset, []imap.FetchItem{imap.FetchEnvelope, imap.FetchUid}, messages)
	}()

	validUIDs := make([]uint32, 0, len(uids))
	timeout := time.NewTimer(defaultIMAPTimeout)
	defer timeout.Stop()

	done := make(chan struct{})
	go func() {
		for msg := range messages { // ends when fetch returns (defer close(ch))
			if msg != nil && msg.Uid > 0 {
				validUIDs = append(validUIDs, msg.Uid)
			}
		}
		close(done)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			slog.Error("UID validation fetch failed", "error", err, "uids", uids)
			return nil, fmt.Errorf("UID validation fetch failed: %w", err)
		}
		<-done // ensure channel drained
	case <-timeout.C:
		slog.Warn("UID validation timed out", "timeout", defaultIMAPTimeout)
		return nil, fmt.Errorf("UID validation timed out after %v", defaultIMAPTimeout)
	}

	slog.Debug("UID validation complete", "requested", len(uids), "valid", len(validUIDs))
	return validUIDs, nil
}

// fetchSingleMessage fetches a single message with full body and checks if it matches filters
func fetchSingleMessage(client *client.Client, uid uint32, filters []string) (*MailSummary, bool, error) {
	slog.Debug("Fetching individual message with UID", "requested_uid", uid)

	seqset := new(imap.SeqSet)
	seqset.AddNum(uid)

	section := &imap.BodySectionName{Peek: true} // BODY.PEEK[] to avoid marking as read
	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchUid, section.FetchItem()}

	messages := make(chan *imap.Message, 1)
	errCh := make(chan error, 1)

	// Kick off the fetch
	go func() { errCh <- client.UidFetch(seqset, items, messages) }()

	// Read the message (and its body) while the fetch is in flight
	timeout := 30 * time.Second
	var msg *imap.Message
	select {
	case msg = <-messages:
		if msg == nil {
			return nil, false, fmt.Errorf("no message received for UID %d", uid)
		}
	case err := <-errCh: // server replied quickly but no message?
		if err != nil {
			return nil, false, fmt.Errorf("failed to fetch message %d: %w", uid, err)
		}
		// fall through to also read any final message (if any)
		select {
		case msg = <-messages:
			if msg == nil {
				return nil, false, fmt.Errorf("no message received for UID %d", uid)
			}
		case <-time.After(timeout):
			return nil, false, fmt.Errorf("timed out waiting for message %d", uid)
		}
	case <-time.After(timeout):
		return nil, false, fmt.Errorf("IMAP UID fetch timed out after %v", timeout)
	}

	// Filter
	matches := isFromAddressMatching(msg.Envelope, filters)
	if !matches {
		slog.Debug("Message does not match filter", "uid", uid, "from", getFromAddress(msg.Envelope))
		// drain/allow the command to complete
		go func() {
			if _, err := io.Copy(io.Discard, msg.GetBody(section)); err != nil {
				slog.Debug("Failed to drain message body", "uid", uid, "error", err)
			}
		}()
		return nil, false, nil
	}

	// IMPORTANT: read the body to keep the parser unblocked
	body := msg.GetBody(section)
	if body == nil {
		return nil, true, fmt.Errorf("no body found for message %d", uid)
	}
	// Ensure body is always drained to prevent wedging the parser
	defer func() { _, _ = io.Copy(io.Discard, body) }()

	entity, err := message.Read(body) // this consumes the literal stream
	if err != nil {
		return nil, true, fmt.Errorf("failed to parse message %d: %w", uid, err)
	}

	text, html, attachments := extractBodies(entity)

	// Optionally wait for the fetch to fully finish (so parser drains)
	select {
	case err := <-errCh:
		if err != nil {
			slog.Warn("Fetch finished with error after body read", "uid", uid, "err", err)
		}
	default:
	}

	return &MailSummary{
		Envelope:    msg.Envelope,
		UID:         msg.Uid,
		TextBody:    text,
		HTMLBody:    html,
		Attachments: attachments,
	}, true, nil
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
	errCh := make(chan error, 1)

	// Start fetch in goroutine to avoid deadlock
	go func() {
		errCh <- client.UidFetch(seqset, []imap.FetchItem{imap.FetchEnvelope, imap.FetchUid}, messages)
	}()

	timeout := time.NewTimer(defaultIMAPTimeout)
	defer timeout.Stop()

	done := make(chan struct{})
	go func() {
		slog.Debug("Non-matching unread messages:")
		for msg := range messages { // ends when fetch returns (defer close(ch))
			if msg == nil {
				continue
			}
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
		close(done)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			slog.Debug("Failed to fetch non-matching message envelopes", "error", err)
			return
		}
		<-done // ensure channel drained
	case <-timeout.C:
		slog.Debug("Timed out fetching non-matching message envelopes", "timeout", defaultIMAPTimeout)
		return
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
