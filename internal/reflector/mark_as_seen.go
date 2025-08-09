package reflector

import (
	"fmt"
	"log/slog"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

func markAsSeen(c *client.Client, uid uint32) error {
	slog.Debug("Marking message as seen", "uid", uid)

	seqset := new(imap.SeqSet)
	seqset.AddNum(uid)

	item := imap.FormatFlagsOp(imap.AddFlags, true) // true = silent update
	flags := []any{imap.SeenFlag}

	if err := c.UidStore(seqset, item, flags, nil); err != nil {
		slog.Error("Failed to mark message as seen", "uid", uid, "error", err)
		// Log the current mailbox state for debugging
		slog.Error("IMAP connection might be in wrong state - this suggests INBOX is in EXAMINE mode instead of SELECT mode")
		return fmt.Errorf("failed to mark message %d as \\Seen: %w", uid, err)
	}

	slog.Debug("Successfully marked message as seen", "uid", uid)
	return nil
}
