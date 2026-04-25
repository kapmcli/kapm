package frontmatter

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxFrontmatterBytes = 256 << 10 // 256 KiB

// Document is a markdown-like file split into YAML front matter and body.
type Document struct {
	Meta map[string]any
	Body string
}

// Parse separates leading YAML front matter from the remaining body.
func Parse(content string) (Document, error) {
	remaining, ok := cutOpeningDelimiter(content)
	if !ok {
		return Document{Body: content}, nil
	}

	var (
		metaText string
		body     string
	)

	var found bool
	metaText, body, found = cutClosingDelimiter(remaining)
	if !found {
		return Document{}, fmt.Errorf("front matter parse: missing closing delimiter")
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

func cutOpeningDelimiter(content string) (string, bool) {
	switch {
	case strings.HasPrefix(content, "---\r\n"):
		return content[len("---\r\n"):], true
	case strings.HasPrefix(content, "---\n"):
		return content[len("---\n"):], true
	default:
		return "", false
	}
}

func cutClosingDelimiter(content string) (metaText, body string, found bool) {
	lineStart := 0
	for lineStart <= len(content) {
		lineEnd := strings.IndexByte(content[lineStart:], '\n')
		if lineEnd < 0 {
			line := strings.TrimSuffix(content[lineStart:], "\r")
			if line == "---" {
				return strings.TrimSuffix(content[:lineStart], "\r\n"), "", true
			}
			return "", "", false
		}

		lineEnd += lineStart
		line := strings.TrimSuffix(content[lineStart:lineEnd], "\r")
		if line == "---" {
			return strings.TrimSuffix(content[:lineStart], "\r\n"), content[lineEnd+1:], true
		}
		lineStart = lineEnd + 1
	}

	return "", "", false
}
