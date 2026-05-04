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
	Port         int
	SessionsDir  string
	LogsDir      string
	IDEBaseDir   string
	CwdFilter    string
	Since        time.Duration
	MetricsTTL   time.Duration // 0 means default 1s
	SQLiteDBPath string
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
	sqliteCache  *monitor.SQLiteCache
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
	s := &Server{opts: opts, now: time.Now, cache: monitor.NewSessionCache(), sqliteCache: monitor.NewSQLiteCache(), ttl: ttl}
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

const (
	httpReadTimeout       = 10 * time.Second
	httpReadHeaderTimeout = 5 * time.Second
	httpIdleTimeout       = 60 * time.Second
)

const (
	dashboardSessionLimit = 50
	sessionsPerPage       = 50
)
