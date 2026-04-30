package serve

import (
	"fmt"
	"html"
	"html/template"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	udiff "github.com/aymanbagabas/go-udiff"

	"github.com/kapmcli/kapm/internal/monitor"
)

const diffByteCap = 64 << 10

var hunkHeaderRE = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

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
	return renderDiffString(diffStr)
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

// DiffStatsResult is the aggregated +/- summary for a group of FileChanges.
// HasCounts is false when all edits are oversized/unknown and no counts could
// be computed; in that case the UI should render a muted em-dash.
type DiffStatsResult struct {
	Adds, Dels     int
	HasCounts      bool
	OversizedCount int
}

// diffStats aggregates DiffLineCounts across changes. Edits whose counts
// cannot be computed (Oversized or unknown command) contribute to
// OversizedCount only.
func diffStats(changes []monitor.FileChange) DiffStatsResult {
	var r DiffStatsResult
	for _, c := range changes {
		adds, dels, ok := monitor.DiffLineCounts(c)
		if !ok {
			r.OversizedCount++
			continue
		}
		r.Adds += adds
		r.Dels += dels
		r.HasCounts = true
	}
	return r
}

// editDiffStats returns DiffStatsResult for a single FileChange.
func editDiffStats(fc monitor.FileChange) DiffStatsResult {
	adds, dels, ok := monitor.DiffLineCounts(fc)
	if !ok {
		return DiffStatsResult{OversizedCount: 1}
	}
	return DiffStatsResult{Adds: adds, Dels: dels, HasCounts: true}
}

// renderDiffString converts a unified diff string into styled HTML.
// Shared by renderDiff (single edit) and mergeChanges (merged).
func renderDiffString(diffStr string) template.HTML {
	var b strings.Builder
	b.WriteString(`<pre class="diff">`)
	var oldLn, newLn int
	for line := range strings.SplitSeq(diffStr, "\n") {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") || strings.HasPrefix(line, `\ `) {
			continue
		}
		if m := hunkHeaderRE.FindStringSubmatch(line); m != nil {
			oldLn, _ = strconv.Atoi(m[1])
			newLn, _ = strconv.Atoi(m[2])
			b.WriteString(`<div class="diff-hunk">`)
			b.WriteString(html.EscapeString(line))
			b.WriteString(`</div>`)
			continue
		}
		if len(line) == 0 {
			continue
		}
		var cls, sign, oldCol, newCol string
		switch line[0] {
		case '+':
			cls = "diff-add"
			sign = "+"
			newCol = strconv.Itoa(newLn)
			newLn++
		case '-':
			cls = "diff-del"
			sign = "-"
			oldCol = strconv.Itoa(oldLn)
			oldLn++
		default:
			cls = "diff-ctx"
			sign = " "
			oldCol = strconv.Itoa(oldLn)
			newCol = strconv.Itoa(newLn)
			oldLn++
			newLn++
		}
		code := ""
		if len(line) > 1 {
			code = line[1:]
		}
		fmt.Fprintf(&b, `<div class="diff-row %s"><span class="ln ln-old">%s</span><span class="ln ln-new">%s</span><span class="sign">%s</span><span class="code">%s</span></div>`,
			cls, oldCol, newCol, html.EscapeString(sign), html.EscapeString(code))
	}
	b.WriteString(`</pre>`)
	return template.HTML(b.String()) //nolint:gosec // all content escaped via html.EscapeString above
}
