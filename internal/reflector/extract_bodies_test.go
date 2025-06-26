package reflector

import (
	"strings"
	"testing"

	"github.com/emersion/go-message"
)

func TestExtractBodies_TextAndHtml(t *testing.T) {
	t.Parallel()

	raw := `Content-Type: multipart/alternative; boundary="xyz"

--xyz
Content-Type: text/plain

This is the plain text version.

--xyz
Content-Type: text/html

<b>This is the HTML version.</b>

--xyz--`

	entity, err := message.Read(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("failed to parse message: %v", err)
	}

	text, html, attachments := extractBodies(entity)

	if text != "This is the plain text version.\n" {
		t.Errorf("unexpected text body: %q", text)
	}

	if html != "<b>This is the HTML version.</b>\n" {
		t.Errorf("unexpected HTML body: %q", html)
	}

	if len(attachments) != 0 {
		t.Errorf("unexpected attachments found")
	}
}
