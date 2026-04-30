package serve

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"strconv"
	"time"

	"github.com/kapmcli/kapm/internal/monitor"
)

type navItem struct {
	Key, Label, Href string
}

// navItems is the fixed top-nav link list shared across pages.
var navItems = []navItem{
	{Key: "overview", Label: "Overview", Href: "/"},
	{Key: "sessions", Label: "Sessions", Href: "/sessions"},
	{Key: "agents", Label: "Agents", Href: "/agents"},
	{Key: "tools", Label: "Tools", Href: "/tools"},
	{Key: "skills", Label: "Skills", Href: "/skills"},
}

// templateFuncs are the helpers callable from templates.
var templateFuncs = template.FuncMap{
	"add": func(a, b int) int { return a + b },
	"sub": func(a, b int) int { return a - b },
	"mul": func(a, b float64) float64 { return a * b },
	"div": func(a, b float64) float64 {
		if b == 0 {
			return 0
		}
		return a / b
	},
	"itof": func(a int) float64 { return float64(a) },
	"addf": func(a, b float64) float64 { return a + b },
	"fmtTokens": func(n int) string {
		switch {
		case n >= 1_000_000:
			return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
		case n >= 1_000:
			return fmt.Sprintf("%.1fK", float64(n)/1_000)
		default:
			return strconv.Itoa(n)
		}
	},
	"fmtCredits": func(v float64) string {
		if v == 0 {
			return "—"
		}
		return fmt.Sprintf("%.2f", v)
	},
	"dur":                func(d monitor.JSONDuration) string { return monitor.FormatDuration(time.Duration(d)) },
	"renderMarkdown":     renderMarkdown,
	"renderDiff":         renderDiff,
	"hasShellEvent":      monitor.HasShellEvent,
	"groupChangesByPath": groupChangesByPath,
	"diffStats":          diffStats,
	"editDiffStats":      editDiffStats,
	"shortID": func(id string, n int) string {
		if len(id) <= n {
			return id
		}
		return id[:n]
	},
	// localtime renders a <time> element with js-localtime class.
	// Safe to return template.HTML: all values come from time.Time.Format with hardcoded format strings.
	"localtime": func(t time.Time, format string) template.HTML {
		utc := t.UTC().Format("2006-01-02T15:04:05Z")
		display := t.UTC().Format("2006-01-02 15:04:05 UTC")
		return template.HTML(fmt.Sprintf(`<time class="js-localtime" datetime="%s" data-format="%s">%s</time>`, utc, html.EscapeString(format), display))
	},
	// truncTitle renders a table cell with truncated text and a title tooltip.
	"truncTitle": func(title string) template.HTML {
		display := title
		if display == "" {
			display = "—"
		}
		return template.HTML(fmt.Sprintf(`<td class="truncate" title="%s">%s</td>`, html.EscapeString(title), html.EscapeString(display)))
	},
}

// parsePage returns a template parsed from layout.html + _partials.html + the named page template.
func parsePage(name string) *template.Template {
	return template.Must(
		template.New(name).Funcs(templateFuncs).ParseFS(Templates, "templates/layout.html", "templates/_partials.html", "templates/"+name+".html"),
	)
}

var (
	overviewTmpl      = parsePage("overview")
	sessionsTmpl      = parsePage("sessions")
	sessionDetailTmpl = parsePage("session_detail")
	agentsTmpl        = parsePage("agents")
	agentDetailTmpl   = parsePage("agent_detail")
	toolsTmpl         = parsePage("tools")
	toolDetailTmpl    = parsePage("tool_detail")
	skillsTmpl        = parsePage("skills")
	errorTmpl         = parsePage("error")
	designPreviewTmpl = template.Must(template.New("design_preview").ParseFS(Templates, "templates/design_preview.html"))
)

// marshalForTemplate serializes v as JSON wrapped in template.JS for embedding
// in HTML via html/template. Returns an error for the caller to propagate.
func marshalForTemplate(v any) (template.JS, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return template.JS(b), nil //nolint:gosec // json.Marshal escapes <,>,& so </script> injection is impossible
}
