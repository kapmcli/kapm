package serve

import (
	"html"
	"html/template"
	"strings"
	"unicode/utf8"

	udiff "github.com/aymanbagabas/go-udiff"

	"github.com/kapmcli/kapm/internal/monitor"
)

const diffByteCap = 64 << 10

func renderDiff(fc monitor.FileChange) template.HTML {
	if fc.Oversized {
		return `<pre class="diff"><em class="muted">(content truncated at extraction — diff unavailable)</em></pre>`
	}
	total := len(fc.Content) + len(fc.OldStr) + len(fc.NewStr)
	if total > diffByteCap {
		return template.HTML(`<pre class="diff"><em class="muted">(diff omitted — content size exceeds 64KB)</em></pre>`)
	}
	if !validUTF8Fields(fc) {
		return `<pre class="diff"><em class="muted">(binary or non-UTF8 content — diff omitted)</em></pre>`
	}

	var diffStr string
	switch fc.Command {
	case "create", "insert":
		diffStr = udiff.Unified(fc.Path, fc.Path, "", fc.Content)
	case "strReplace":
		diffStr = udiff.Unified(fc.Path, fc.Path, fc.OldStr, fc.NewStr)
	}
	if diffStr == "" {
		return `<pre class="diff"><em class="muted">(no textual change)</em></pre>`
	}

	var b strings.Builder
	b.WriteString(`<pre class="diff">`)
	for _, line := range strings.Split(diffStr, "\n") {
		cls := classifyDiffLine(line)
		b.WriteString(`<span class="`)
		b.WriteString(cls)
		b.WriteString(`">`)
		b.WriteString(html.EscapeString(line))
		b.WriteString("\n</span>")
	}
	b.WriteString(`</pre>`)
	return template.HTML(b.String()) //nolint:gosec // all content escaped via html.EscapeString above
}

func classifyDiffLine(line string) string {
	if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "@@") {
		return "diff-hunk"
	}
	if len(line) == 0 {
		return "diff-ctx"
	}
	switch line[0] {
	case '+':
		return "diff-add"
	case '-':
		return "diff-del"
	default:
		return "diff-ctx"
	}
}

func validUTF8Fields(fc monitor.FileChange) bool {
	return utf8.ValidString(fc.Content) && utf8.ValidString(fc.OldStr) && utf8.ValidString(fc.NewStr)
}

// groupChangesByPath groups changes by their Path field.
func groupChangesByPath(changes []monitor.FileChange) map[string][]monitor.FileChange {
	m := make(map[string][]monitor.FileChange, len(changes))
	for _, c := range changes {
		m[c.Path] = append(m[c.Path], c)
	}
	return m
}
