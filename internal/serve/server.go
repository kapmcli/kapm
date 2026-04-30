// Package serve implements the kapm WebUI HTTP server.
package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"

	"github.com/kapmcli/kapm/internal/monitor"
	"github.com/kapmcli/kapm/internal/stylegen"
)

// aggregateDetailFn is the function used to compute DetailedMetrics.
// Overridable in tests via export_test.go.
var aggregateDetailFn = monitor.AggregateDetail

// loadedMetrics bundles the result of a single loadMetrics call.
type loadedMetrics struct {
	dm       monitor.DetailedMetrics
	sessions []monitor.SessionMetric
}

// agentLink is a {agent, URL} pair rendered next to a merged session view.
// URL construction lives here so internal/monitor stays URL-free.
type agentLink struct {
	Agent string
	URL   string
}

// buildAgentLinks returns URL links for each agent ref, escaping the agent
// name so values like "(unknown)" survive a URL round-trip.
func buildAgentLinks(id string, refs []monitor.AgentRef) []agentLink {
	links := make([]agentLink, 0, len(refs))
	for _, r := range refs {
		links = append(links, agentLink{
			Agent: r.Agent,
			URL:   "/sessions/" + id + "/" + url.PathEscape(r.Agent),
		})
	}
	return links
}

// navItems is the fixed top-nav link list shared across pages.
var navItems = []navItem{
	{Key: "overview", Label: "Overview", Href: "/"},
	{Key: "sessions", Label: "Sessions", Href: "/sessions"},
	{Key: "agents", Label: "Agents", Href: "/agents"},
	{Key: "tools", Label: "Tools", Href: "/tools"},
	{Key: "skills", Label: "Skills", Href: "/skills"},
}

type navItem struct {
	Key, Label, Href string
}

// linkRelRenderer adds rel="noopener nofollow" to all <a> tags via goldmark NodeRenderer.
type linkRelRenderer struct{ gmhtml.Config }

func (r *linkRelRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindLink, r.renderLink)
	reg.Register(ast.KindAutoLink, r.renderAutoLink)
}

func (r *linkRelRenderer) renderAutoLink(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	al := n.(*ast.AutoLink)
	u := util.EscapeHTML(util.URLEscape(al.URL(source), false))
	_, _ = w.WriteString(`<a rel="noopener nofollow" href="`)
	_, _ = w.Write(u)
	_, _ = w.WriteString(`">`)
	_, _ = w.Write(util.EscapeHTML(al.Label(source)))
	_, _ = w.WriteString(`</a>`)
	return ast.WalkSkipChildren, nil
}

// schemeOf returns the lower-cased scheme and whether dest is a relative URL.
// A URL is considered relative when it has no ":" before the first "/" or "?".
func schemeOf(dest string) (scheme string, isRelative bool) {
	s := strings.TrimSpace(dest)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ':':
			return strings.ToLower(s[:i]), false
		case '/', '?', '#':
			return "", true
		}
	}
	return "", true
}

func (r *linkRelRenderer) renderLink(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString("</a>")
		return ast.WalkContinue, nil
	}
	link := n.(*ast.Link)
	dest := string(link.Destination)
	scheme, isRel := schemeOf(dest)
	switch {
	case isRel, scheme == "http", scheme == "https", scheme == "mailto":
		// allowed
	default:
		_, _ = w.WriteString(`<a rel="noopener nofollow" href="#">`)
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString(`<a rel="noopener nofollow" href="`)
	_, _ = w.Write(util.EscapeHTML(util.URLEscape(link.Destination, true)))
	_, _ = w.WriteString(`">`)
	return ast.WalkContinue, nil
}

var mdRenderer = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(gmhtml.WithHardWraps()),
	goldmark.WithRenderer(renderer.NewRenderer(
		renderer.WithNodeRenderers(
			util.Prioritized(gmhtml.NewRenderer(), 1000),
			util.Prioritized(&linkRelRenderer{}, 100),
		),
	)),
)

// renderMarkdown converts markdown s to safe HTML. Raw HTML is escaped because
// goldmark WithUnsafe is OFF. Empty input returns a placeholder element.
func renderMarkdown(s string) template.HTML {
	if strings.TrimSpace(s) == "" {
		return `<em class="muted">(empty)</em>`
	}
	var buf bytes.Buffer
	if err := mdRenderer.Convert([]byte(s), &buf); err != nil {
		return template.HTML(html.EscapeString(s))
	}
	//nolint:gosec // goldmark WithUnsafe OFF keeps raw HTML escaped
	return template.HTML(buf.String())
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

// Options configures a Server.
type Options struct {
	Port        int
	SessionsDir string
	LogsDir     string
	IDEBaseDir  string
	CwdFilter   string
	Since       time.Duration
	MetricsTTL  time.Duration // 0 means default 1s
	// MaxSSE caps concurrent SSE connections. 0 means default 64.
	MaxSSE int
}

// metricsCacheEntry holds a cached DetailedMetrics result with its generation time.
type metricsCacheEntry struct {
	dm                monitor.DetailedMetrics
	dashboardSessions []monitor.SessionMetric
	storedAt          time.Time
}

// Server serves the kapm WebUI.
type Server struct {
	opts         Options
	now          func() time.Time
	handler      http.Handler
	cache        *monitor.SessionCache
	ttl          time.Duration
	metricsMu    sync.Mutex
	metricsCache *metricsCacheEntry
	metricsSF    singleflight.Group
	sseMax       int32
	sseCount     atomic.Int32
}

// New constructs a Server with the given options.
func New(opts Options) *Server {
	ttl := opts.MetricsTTL
	if ttl == 0 {
		ttl = time.Second
	}
	s := &Server{opts: opts, now: time.Now, cache: monitor.NewSessionCache(), ttl: ttl}
	s.sseMax = defaultMaxSSE
	if opts.MaxSSE > 0 {
		if opts.MaxSSE > math.MaxInt32 {
			opts.MaxSSE = math.MaxInt32
		}
		s.sseMax = int32(opts.MaxSSE)
	}
	s.handler = s.buildHandler()
	return s
}

// Addr returns the TCP address the server binds to.
func (s *Server) Addr() string {
	return fmt.Sprintf("127.0.0.1:%d", s.opts.Port)
}

// Handler returns the configured HTTP handler (exposed for tests).
func (s *Server) Handler() http.Handler { return s.handler }

// Run listens on 127.0.0.1:<Port> and serves until ctx is canceled.
// It returns a clear error on port conflict.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.Addr())
	if err != nil {
		return fmt.Errorf("serve listen %q: %w", s.Addr(), err)
	}
	srv := &http.Server{
		Handler:           s.handler,
		ReadTimeout:       httpReadTimeout,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		WriteTimeout:      0, // SSE streams: no write deadline
		IdleTimeout:       httpIdleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// best-effort graceful shutdown; connection cleanup still happens on timeout
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		return err
	}
}

// buildHandler constructs the HTTP mux with all routes wrapped by security headers.
func (s *Server) buildHandler() http.Handler {
	mux := http.NewServeMux()

	// Static assets.
	assetFS, err := fs.Sub(Assets, "assets")
	if err != nil {
		// embed guarantees this subtree exists; fall back to Assets root.
		assetFS = Assets
	}
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(assetFS)))

	// HTML pages.
	mux.HandleFunc("GET /{$}", s.handleDashboard)
	mux.HandleFunc("GET /sessions", s.handleSessions)
	mux.HandleFunc("GET /sessions/{id}", s.handleSessionDetail)
	mux.HandleFunc("GET /sessions/{id}/{agent}", s.handleSessionAgentDetail)
	mux.HandleFunc("GET /agents", s.handleAgents)
	mux.HandleFunc("GET /agents/{name}", s.handleAgentDetail)
	mux.HandleFunc("GET /tools", s.handleTools)
	mux.HandleFunc("GET /tools/{name}", s.handleToolDetail)
	mux.HandleFunc("GET /skills", s.handleSkills)
	mux.HandleFunc("GET /design-preview", s.handleDesignPreview)

	// JSON API and SSE.
	mux.HandleFunc("GET /api/metrics", s.handleAPIMetrics)
	mux.HandleFunc("GET /sse", s.handleSSE)

	// 404 for everything else.
	mux.HandleFunc("/", s.handleNotFound)

	return securityHeaders(mux)
}

const cspHeader = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; font-src 'self'; frame-ancestors 'none'"

// securityHeaders sets common security response headers on every request.
// CSP is applied to all paths except /sse (SSE streams must not be interrupted).
func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		if r.URL.Path != "/sse" {
			w.Header().Set("Content-Security-Policy", cspHeader)
		}
		h.ServeHTTP(w, r)
	})
}

// loadMetrics reads and aggregates records for the configured window.
// Results are cached for s.ttl to avoid repeated AggregateDetail calls.
// Only one goroutine runs AggregateDetail per TTL window (singleflight).
// ctx is forwarded to the record load and aggregation so request cancellation
// aborts in-flight work.
func (s *Server) loadMetrics(ctx context.Context) (loadedMetrics, error) {
	now := s.now()

	s.metricsMu.Lock()
	if s.metricsCache != nil && now.Sub(s.metricsCache.storedAt) < s.ttl {
		entry := s.metricsCache
		s.metricsMu.Unlock()
		return loadedMetrics{dm: entry.dm, sessions: entry.dashboardSessions}, nil
	}
	s.metricsMu.Unlock()

	v, err, _ := s.metricsSF.Do("metrics", func() (any, error) {
		// Re-check: another goroutine may have populated the cache while we waited.
		s.metricsMu.Lock()
		if s.metricsCache != nil && now.Sub(s.metricsCache.storedAt) < s.ttl {
			entry := s.metricsCache
			s.metricsMu.Unlock()
			return loadedMetrics{dm: entry.dm, sessions: entry.dashboardSessions}, nil
		}
		s.metricsMu.Unlock()

		recs, nextCache, err := monitor.LoadAll(ctx, s.opts.SessionsDir, s.opts.LogsDir, s.opts.IDEBaseDir, now.Add(-s.opts.Since), s.opts.CwdFilter, s.cache)
		if err != nil {
			return loadedMetrics{}, fmt.Errorf("serve load records: %w", err)
		}
		s.cache = nextCache
		dm, err := aggregateDetailFn(ctx, recs, now)
		if err != nil {
			return loadedMetrics{}, fmt.Errorf("serve aggregate records: %w", err)
		}
		sessions := computeDashboardSessions(dm.Overview.Sessions)

		s.metricsMu.Lock()
		s.metricsCache = &metricsCacheEntry{dm: dm, dashboardSessions: sessions, storedAt: now}
		s.metricsMu.Unlock()

		return loadedMetrics{dm: dm, sessions: sessions}, nil
	})
	if err != nil {
		return loadedMetrics{}, err
	}
	lm, ok := v.(loadedMetrics)
	if !ok {
		return loadedMetrics{}, fmt.Errorf("unexpected metrics type %T", v)
	}
	return lm, nil
}

// handleAPIMetrics returns the full DetailedMetrics (optionally filtered) as JSON.
func (s *Server) handleAPIMetrics(w http.ResponseWriter, r *http.Request) {
	loaded, err := s.loadMetrics(r.Context())
	if err != nil {
		s.handleError(w, r, err, http.StatusInternalServerError)
		return
	}
	dm := loaded.dm

	// Optional filters: ?session=<id> or ?agent=<name> narrows the response.
	if sid := r.URL.Query().Get("session"); sid != "" {
		dm = filterBySession(dm, sid)
	} else if ag := r.URL.Query().Get("agent"); ag != "" {
		dm = filterByAgent(dm, ag)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(dm); err != nil {
		slog.Warn("serve encode metrics", "err", err)
	}
}

// handleSSE streams Overview summaries (not full DetailedMetrics) every 5s.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	if s.sseCount.Add(1) > s.sseMax {
		s.sseCount.Add(-1)
		w.Header().Set("Retry-After", "1")
		http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
		return
	}
	defer s.sseCount.Add(-1)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "sse not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	if !s.sendOverview(w, flusher, r) {
		return
	}

	ticker := time.NewTicker(sseStreamInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if !s.sendOverview(w, flusher, r) {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

// buildSSEFrames computes SSE payload bytes from loaded metrics.
func buildSSEFrames(lm loadedMetrics) (summaryHTML, overviewJSON []byte, err error) {
	var htmlBuf bytes.Buffer
	if err := overviewTmpl.ExecuteTemplate(&htmlBuf, "summary-cards", lm.dm.Overview); err != nil {
		return nil, nil, fmt.Errorf("serve sse render summary: %w", err)
	}
	summaryHTML = bytes.ReplaceAll(htmlBuf.Bytes(), []byte("\n"), []byte(" "))

	overviewJSON, err = json.Marshal(lm.dm.Overview)
	if err != nil {
		return nil, nil, fmt.Errorf("serve sse marshal overview: %w", err)
	}
	return summaryHTML, overviewJSON, nil
}

// sendOverview writes SSE frames with the current overview snapshot:
//   - `event: summary` with rendered summary-cards HTML (for htmx sse-swap)
//   - `event: overview` with Overview JSON (for ECharts update in client JS)
func (s *Server) sendOverview(w io.Writer, flusher http.Flusher, r *http.Request) bool {
	lm, err := s.loadMetrics(r.Context())
	if err != nil {
		slog.Warn("serve sse load metrics", "err", err)
		return false
	}

	htmlFrame, jsonFrame, err := buildSSEFrames(lm)
	if err != nil {
		slog.Warn("serve sse build frames", "err", err)
		return false
	}

	if _, err := fmt.Fprintf(w, "event: summary\ndata: %s\n\n", htmlFrame); err != nil {
		s.logWriteFailure(r, "sse summary", fmt.Errorf("send sse summary frame: %w", err))
		return false
	}
	if _, err := fmt.Fprintf(w, "event: overview\ndata: %s\n\n", jsonFrame); err != nil {
		s.logWriteFailure(r, "sse overview", fmt.Errorf("send sse overview frame: %w", err))
		return false
	}
	flusher.Flush()
	return true
}

// currentUpdatedAt returns the header "updated:" timestamp. If KAPM_UPDATED_AT
// is set it takes precedence (used by vhs-test and playwright for stable
// goldens); otherwise the current wall-clock time is formatted as HH:MM:SS.
func currentUpdatedAt() string {
	if v := os.Getenv("KAPM_UPDATED_AT"); v != "" {
		return v
	}
	return time.Now().Format("15:04:05")
}

// renderPage executes tmpl's "layout" (or "content" + OOB "nav" for htmx
// requests so the active nav link updates) and writes status on success.
func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, status int, tmpl *template.Template, data map[string]any) {
	if _, ok := data["Nav"]; !ok {
		data["Nav"] = navItems
	}
	data["UpdatedAt"] = currentUpdatedAt()
	var buf bytes.Buffer
	isHX := r != nil && r.Header.Get("HX-Request") == "true"
	if isHX {
		if err := tmpl.ExecuteTemplate(&buf, "content", data); err != nil {
			s.handleError(w, r, fmt.Errorf("render page content template %q: %w", tmpl.Name(), err), http.StatusInternalServerError)
			return
		}
		// Out-of-band swap updates the nav's active link alongside #content.
		buf.WriteString(`<nav id="main-nav" hx-swap-oob="true">`)
		if err := tmpl.ExecuteTemplate(&buf, "nav", data); err != nil {
			s.handleError(w, r, fmt.Errorf("render page nav template: %w", err), http.StatusInternalServerError)
			return
		}
		buf.WriteString(`</nav>`)
		// OOB swap for the header's updated-at stamp so htmx navigation
		// refreshes the timestamp without a full reload.
		fmt.Fprintf(&buf, `<span id="updated-at" class="updated-at" hx-swap-oob="true">updated: %s</span>`, html.EscapeString(fmt.Sprint(data["UpdatedAt"])))
		// OOB swap for the browser tab title on htmx navigation.
		if titleStr, _ := data["Title"].(string); titleStr != "" {
			fmt.Fprintf(&buf, `<title hx-swap-oob="true">%s — kapm</title>`, html.EscapeString(titleStr))
		}
	} else {
		if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
			s.handleError(w, r, err, http.StatusInternalServerError)
			return
		}
	}
	s.writeHTML(w, r, status, buf.Bytes())
}

func (s *Server) writeHTML(w http.ResponseWriter, r *http.Request, status int, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		s.logWriteFailure(r, "html response", err)
	}
}

func (s *Server) logWriteFailure(r *http.Request, context string, err error) {
	attrs := []any{"context", context, "err", err}
	if r != nil {
		attrs = append(attrs, "method", r.Method, "path", r.URL.Path)
	}
	slog.Warn("serve write failed", attrs...)
}

// computeDashboardSessions groups sessions by ID for the dashboard's Recent
// Sessions panel. Within each ID group rows are ordered by LastActivity desc;
// groups themselves are ordered by their most-recent LastActivity desc so the
// panel stays newest-first. The input slice is not modified.
func computeDashboardSessions(sessions []monitor.SessionMetric) []monitor.SessionMetric {
	out := slices.Clone(sessions)
	groupLast := map[string]time.Time{}
	for _, s := range out {
		if s.LastActivity.After(groupLast[s.ID]) {
			groupLast[s.ID] = s.LastActivity
		}
	}
	slices.SortStableFunc(out, func(a, b monitor.SessionMetric) int {
		if a.ID != b.ID {
			if c := groupLast[b.ID].Compare(groupLast[a.ID]); c != 0 {
				return c
			}
			return strings.Compare(a.ID, b.ID)
		}
		return b.LastActivity.Compare(a.LastActivity)
	})
	return out
}

// marshalForTemplate serializes v as JSON wrapped in template.JS for embedding
// in HTML via html/template. Returns an error for the caller to propagate.
func marshalForTemplate(v any) (template.JS, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return template.JS(b), nil //nolint:gosec // json.Marshal escapes <,>,& so </script> injection is impossible
}

// withMetrics calls fn with the current metrics. If loading fails, it
// writes an error response and fn is not called.
func (s *Server) withMetrics(w http.ResponseWriter, r *http.Request, fn func(loadedMetrics)) {
	loaded, err := s.loadMetrics(r.Context())
	if err != nil {
		s.handleError(w, r, err, http.StatusInternalServerError)
		return
	}
	fn(loaded)
}

// handleDashboard renders the Overview page from embedded templates.
// Overview.Sessions is capped to dashboardSessionLimit distinct session IDs
// (paginateByID page=1 semantics) so the Recent Sessions panel stays bounded.
// Depends on computeDashboardSessions returning rows in LastActivity-desc order
// so that page 1 of 50 is the most-recent 50 distinct IDs.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	s.withMetrics(w, r, func(loaded loadedMetrics) {
		capped, _ := paginateByID(loaded.sessions, 1, dashboardSessionLimit)
		overview := loaded.dm.Overview
		overview.Sessions = capped
		overviewJSON, err := marshalForTemplate(overview)
		if err != nil {
			s.handleError(w, r, err, http.StatusInternalServerError)
			return
		}
		s.renderPage(w, r, http.StatusOK, overviewTmpl, map[string]any{
			"Title":        "Overview",
			"Active":       "overview",
			"Overview":     overview,
			"Skills":       loaded.dm.Skills,
			"OverviewJSON": overviewJSON,
		})
	})
}

// handleSessions renders the sessions list page with ?page=N pagination.
// Distinct session IDs are paginated at sessionsPerPage per page.
// Invalid or out-of-range page values are clamped: <1 → 1, >totalPages → totalPages.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	s.withMetrics(w, r, func(loaded loadedMetrics) {
		// Parse ?page= — default 1, clamp <1 to 1.
		requested := 1
		if p := r.URL.Query().Get("page"); p != "" {
			if n, err := strconv.Atoi(p); err == nil && n >= 1 {
				requested = n
			}
		}

		all := loaded.dm.Overview.Sessions
		rows, total := paginateByID(all, requested, sessionsPerPage)

		totalPages := max((total+sessionsPerPage-1)/sessionsPerPage, 1)
		currentPage := requested
		if currentPage > totalPages {
			currentPage = totalPages
			rows, _ = paginateByID(all, currentPage, sessionsPerPage)
		}

		s.renderPage(w, r, http.StatusOK, sessionsTmpl, map[string]any{
			"Title":      "Sessions",
			"Active":     "sessions",
			"Sessions":   rows,
			"Page":       currentPage,
			"TotalPages": totalPages,
			"HasPrev":    currentPage > 1,
			"HasNext":    currentPage < totalPages,
		})
	})
}

// handleSessionDetail serves the merged (all-agents) session detail page;
// 404 if no SessionDetail has that id.
func (s *Server) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.withMetrics(w, r, func(loaded loadedMetrics) {
		matches := sessionDetailsByID(loaded.dm.Sessions, id)
		if len(matches) == 0 {
			s.handleNotFound(w, r)
			return
		}
		merged, refs := monitor.MergeSessionDetails(matches)
		s.renderPage(w, r, http.StatusOK, sessionDetailTmpl, map[string]any{
			"Title":      "Session " + id,
			"Active":     "sessions",
			"Session":    merged,
			"AgentLinks": buildAgentLinks(id, refs),
			"SelfURL":    "/sessions/" + id,
		})
	})
}

// handleSessionAgentDetail serves the per-agent session detail page.
// Returns 400 if the agent segment cannot be URL-decoded, 404 if the
// (id, agent) pair is unknown.
func (s *Server) handleSessionAgentDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rawAgent := r.PathValue("agent")
	agent, err := url.PathUnescape(rawAgent)
	if err != nil {
		s.handleError(w, r, fmt.Errorf("serve decode agent %q: %w", rawAgent, err), http.StatusBadRequest)
		return
	}
	s.withMetrics(w, r, func(loaded loadedMetrics) {
		target, others, ok := sessionDetailByIDAndAgent(loaded.dm.Sessions, id, agent)
		if !ok {
			s.handleNotFound(w, r)
			return
		}
		s.renderPage(w, r, http.StatusOK, sessionDetailTmpl, map[string]any{
			"Title":      "Session " + id + " / " + agent,
			"Active":     "sessions",
			"Session":    target,
			"AgentLinks": buildAgentLinks(id, others),
			"SelfURL":    "/sessions/" + id + "/" + url.PathEscape(agent),
		})
	})
}

// handleAgents renders the agents list page.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	s.withMetrics(w, r, func(loaded loadedMetrics) {
		s.renderPage(w, r, http.StatusOK, agentsTmpl, map[string]any{
			"Title":  "Agents",
			"Active": "agents",
			"Agents": loaded.dm.Agents,
		})
	})
}

// handleAgentDetail serves the agent detail page; 404 if the name is unknown.
func (s *Server) handleAgentDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.withMetrics(w, r, func(loaded loadedMetrics) {
		agent, ok := agentDetailByName(loaded.dm.Agents, name)
		if !ok {
			s.handleNotFound(w, r)
			return
		}
		s.renderPage(w, r, http.StatusOK, agentDetailTmpl, map[string]any{
			"Title":  "Agent " + name,
			"Active": "agents",
			"Agent":  agent,
		})
	})
}

// handleTools renders the tools list page.
func (s *Server) handleTools(w http.ResponseWriter, r *http.Request) {
	s.withMetrics(w, r, func(loaded loadedMetrics) {
		s.renderPage(w, r, http.StatusOK, toolsTmpl, map[string]any{
			"Title":  "Tools",
			"Active": "tools",
			"Tools":  loaded.dm.Tools,
		})
	})
}

// toolDetailVM is the JSON payload injected into /tools/{name} for echarts.
type toolDetailVM struct {
	Timeseries []monitor.TimeseriesPoint `json:"timeseries"`
	Patterns   []monitor.PatternCount    `json:"patterns"`
}

// buildToolDetailVM merges RecentCalls and Errors then aggregates timeseries and patterns.
func buildToolDetailVM(td monitor.ToolDetail, now time.Time) toolDetailVM {
	all := make([]monitor.ToolCall, 0, len(td.RecentCalls)+len(td.Errors))
	all = append(all, td.RecentCalls...)
	all = append(all, td.Errors...)
	return toolDetailVM{
		Timeseries: monitor.AggregateToolTimeseries(all, now),
		Patterns:   monitor.AggregateToolInputPatterns(all, 10),
	}
}

// handleToolDetail serves the tool detail page; 404 if the name is unknown.
func (s *Server) handleToolDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.withMetrics(w, r, func(loaded loadedMetrics) {
		tool, ok := toolDetailByName(loaded.dm.Tools, name)
		if !ok {
			s.handleNotFound(w, r)
			return
		}
		toolDetailJSON, err := marshalForTemplate(buildToolDetailVM(tool, s.now()))
		if err != nil {
			s.handleError(w, r, err, http.StatusInternalServerError)
			return
		}
		s.renderPage(w, r, http.StatusOK, toolDetailTmpl, map[string]any{
			"Title":          "Tool " + name,
			"Active":         "tools",
			"Tool":           tool,
			"ToolDetailJSON": toolDetailJSON,
		})
	})
}

// handleSkills renders the skills list page.
func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	s.withMetrics(w, r, func(loaded loadedMetrics) {
		s.renderPage(w, r, http.StatusOK, skillsTmpl, map[string]any{
			"Title":  "Skills",
			"Active": "skills",
			"Skills": loaded.dm.Skills,
		})
	})
}

// colorEntry is a {name, hex} pair rendered as a swatch on /design-preview.
type colorEntry struct {
	Name string
	Hex  string
}

// handleDesignPreview renders a standalone visualization of the color tokens
// defined in the embedded DESIGN.md. It does not depend on runtime metrics
// or the production style.css.
func (s *Server) handleDesignPreview(w http.ResponseWriter, r *http.Request) {
	d, err := stylegen.ParseDesignMD(DesignMDRaw)
	if err != nil {
		s.handleError(w, r, fmt.Errorf("design parse: %w", err), http.StatusInternalServerError)
		return
	}
	entries := make([]colorEntry, 0, len(stylegen.ColorKeys))
	for _, k := range stylegen.ColorKeys {
		entries = append(entries, colorEntry{Name: k, Hex: d.Colors[k]})
	}
	var buf bytes.Buffer
	if err := designPreviewTmpl.ExecuteTemplate(&buf, "design_preview.html", map[string]any{
		"Design":       d,
		"ColorEntries": entries,
	}); err != nil {
		s.handleError(w, r, fmt.Errorf("design render: %w", err), http.StatusInternalServerError)
		return
	}
	s.writeHTML(w, r, http.StatusOK, buf.Bytes())
}

// handleError logs the full error via slog and writes a generic status response.
// Internal details never reach the client.
func (s *Server) handleError(w http.ResponseWriter, r *http.Request, err error, status int) {
	slog.Warn("serve http error",
		"method", r.Method,
		"path", r.URL.Path,
		"status", status,
		"err", err,
	)
	// Attempt styled error page. Fall back to plain text if template
	// rendering itself fails (avoids infinite recursion since renderPage
	// calls handleError on template errors).
	var buf bytes.Buffer
	data := map[string]any{
		"Title":   http.StatusText(status),
		"Active":  "",
		"Status":  status,
		"Heading": http.StatusText(status),
		"Message": http.StatusText(status),
	}
	if tmplErr := errorTmpl.ExecuteTemplate(&buf, "layout", data); tmplErr == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_, _ = buf.WriteTo(w)
		return
	}
	http.Error(w, http.StatusText(status), status)
}

// handleNotFound renders the error page with HTTP 404.
func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, r, http.StatusNotFound, errorTmpl, map[string]any{
		"Title":   "Not Found",
		"Active":  "",
		"Status":  404,
		"Heading": "Not Found",
		"Message": "The requested resource was not found.",
	})
}

// filterBySession narrows dm to a single session id and returns a merged
// SessionDetail across all agents participating in that sid (see
// monitor.MergeSessionDetails). Tools and Overview.Tools are re-aggregated
// from the pre-merge per-agent timelines via
// monitor.AggregateToolsFromTimeline so tool attribution stays per-agent in
// each ToolCall. Skills are not re-aggregated (session attribution for
// skills is not preserved in DetailedMetrics) and remain empty.
func filterBySession(dm monitor.DetailedMetrics, id string) monitor.DetailedMetrics {
	out := monitor.DetailedMetrics{}
	matches := sessionDetailsByID(dm.Sessions, id)
	if len(matches) == 0 {
		return out
	}
	merged, _ := monitor.MergeSessionDetails(matches)
	out.Sessions = []monitor.SessionDetail{merged}
	out.Overview.Sessions = []monitor.SessionMetric{merged.SessionMetric}
	out.Tools, out.Overview.Tools = monitor.AggregateToolsFromTimeline(matches)
	return out
}

// filterByAgent narrows dm to a single agent name. Tools and Overview.Tools
// are re-aggregated from the agent's session timelines via
// monitor.AggregateToolsFromTimeline. Skills are not re-aggregated (session
// attribution for skills is not preserved in DetailedMetrics) and remain
// empty in the result.
func filterByAgent(dm monitor.DetailedMetrics, name string) monitor.DetailedMetrics {
	out := monitor.DetailedMetrics{}
	for _, a := range dm.Agents {
		if a.Name == name {
			out.Agents = append(out.Agents, a)
			out.Overview.Agents = append(out.Overview.Agents, a.AgentMetric)
		}
	}
	for _, sd := range dm.Sessions {
		if sd.Agent == name {
			out.Sessions = append(out.Sessions, sd)
			out.Overview.Sessions = append(out.Overview.Sessions, sd.SessionMetric)
		}
	}
	out.Tools, out.Overview.Tools = monitor.AggregateToolsFromTimeline(out.Sessions)
	return out
}

func sessionDetailsByID(sessions []monitor.SessionDetail, id string) []monitor.SessionDetail {
	var matches []monitor.SessionDetail
	for _, sd := range sessions {
		if sd.ID == id {
			matches = append(matches, sd)
		}
	}
	return matches
}

func sessionDetailByIDAndAgent(sessions []monitor.SessionDetail, id, agent string) (monitor.SessionDetail, []monitor.AgentRef, bool) {
	var target monitor.SessionDetail
	var others []monitor.AgentRef
	var found bool
	for _, sd := range sessions {
		if sd.ID != id {
			continue
		}
		if sd.Agent == agent {
			target = sd
			found = true
			continue
		}
		others = append(others, monitor.AgentRef{Agent: sd.Agent, AgentKey: sd.AgentKey})
	}
	return target, others, found
}

func agentDetailByName(agents []monitor.AgentDetail, name string) (monitor.AgentDetail, bool) {
	for _, agent := range agents {
		if agent.Name == name {
			return agent, true
		}
	}
	return monitor.AgentDetail{}, false
}

func toolDetailByName(tools []monitor.ToolDetail, name string) (monitor.ToolDetail, bool) {
	for _, tool := range tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return monitor.ToolDetail{}, false
}

// defaultMaxSSE is the default cap for concurrent SSE connections.
const defaultMaxSSE int32 = 64

const (
	sseStreamInterval     = 5 * time.Second
	httpReadTimeout       = 10 * time.Second
	httpReadHeaderTimeout = 5 * time.Second
	httpIdleTimeout       = 60 * time.Second
)

const (
	dashboardSessionLimit = 50
	sessionsPerPage       = 50
)

// paginateByID slices all by distinct session ID, returning the rows for the
// requested page and the total number of distinct IDs. Input order is
// preserved; all rows sharing an ID travel together.
func paginateByID(all []monitor.SessionMetric, page, perPage int) (rows []monitor.SessionMetric, total int) {
	seen := map[string]struct{}{}
	var ids []string
	for _, s := range all {
		if _, ok := seen[s.ID]; !ok {
			seen[s.ID] = struct{}{}
			ids = append(ids, s.ID)
		}
	}
	total = len(ids)
	start := (page - 1) * perPage
	end := start + perPage
	if start >= total {
		return nil, total
	}
	if end > total {
		end = total
	}
	pageIDs := map[string]struct{}{}
	for _, id := range ids[start:end] {
		pageIDs[id] = struct{}{}
	}
	for _, s := range all {
		if _, ok := pageIDs[s.ID]; ok {
			rows = append(rows, s)
		}
	}
	return rows, total
}
