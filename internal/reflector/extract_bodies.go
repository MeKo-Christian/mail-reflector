package reflector

import (
	"io"
	"log/slog"
	"mime"
	"strings"

	"github.com/emersion/go-message"
)

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
			body, err := io.ReadAll(part.Body)
			if err != nil {
				slog.Warn("Failed to read part body", "error", err)

				continue
			}

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
		body, err := io.ReadAll(entity.Body)
		if err != nil {
			slog.Error("Failed to read body", "error", err)
			return "", "", attachments
		}

		switch mediaType {
		case "text/plain":
			text = string(body)
		case "text/html":
			html = string(body)
		}
	}

	return text, html, attachments
}
