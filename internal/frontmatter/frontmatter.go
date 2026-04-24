package frontmatter

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxFrontmatterBytes = 256 << 10 // 256 KiB

const delimiter = "---\n"

// Document is a markdown-like file split into YAML front matter and body.
type Document struct {
	Meta map[string]any
	Body string
}

// Parse separates leading YAML front matter from the remaining body.
func Parse(content string) (Document, error) {
	if !strings.HasPrefix(content, delimiter) {
		return Document{Body: content}, nil
	}

	remaining := content[len(delimiter):]

	var (
		metaText string
		body     string
	)

	switch {
	case strings.HasPrefix(remaining, delimiter):
		body = remaining[len(delimiter):]
	default:
		var found bool
		metaText, body, found = strings.Cut(remaining, "\n"+delimiter)
		if !found {
			return Document{}, fmt.Errorf("front matter parse: missing closing delimiter")
		}
	}

	if metaText == "" {
		return Document{
			Meta: map[string]any{},
			Body: body,
		}, nil
	}

	if int64(len(metaText)) > maxFrontmatterBytes {
		return Document{}, fmt.Errorf("front matter parse: front matter too large (%d bytes, limit %d)", len(metaText), maxFrontmatterBytes)
	}

	meta := make(map[string]any)
	if err := yaml.Unmarshal([]byte(metaText), &meta); err != nil {
		return Document{}, fmt.Errorf("front matter parse: %w", err)
	}

	return Document{
		Meta: meta,
		Body: body,
	}, nil
}
