// Package serve implements the kapm WebUI HTTP server.
package serve

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/kapmcli/kapm/internal/monitor"
)

// aggregateDetailFn is the function used to compute DetailedMetrics.
// Overridable in tests via export_test.go.
var aggregateDetailFn = monitor.AggregateDetail

// loadedMetrics bundles the result of a single loadMetrics call.
type loadedMetrics struct {
	dm       monitor.DetailedMetrics
	sessions []monitor.SessionMetric
}

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

const (
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
