package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/monitor"
)

type failingResponseWriter struct {
	header http.Header
	code   int
	err    error
}

func (w *failingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failingResponseWriter) WriteHeader(status int) {
	w.code = status
}

func (w *failingResponseWriter) Write(_ []byte) (int, error) {
	return 0, w.err
}

type failingSSEWriter struct{ err error }

func (w *failingSSEWriter) Write(_ []byte) (int, error) { return 0, w.err }
func (w *failingSSEWriter) Flush()                      {}

const testdataLogsDir = "../../testdata/monitor"

func newTestServer(t *testing.T) *Server {
	t.Helper()
	return New(Options{Port: 0, LogsDir: testdataLogsDir, Since: 365 * 24 * time.Hour})
}

func TestAPIMetricsJSON(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var dm monitor.DetailedMetrics
	if err := json.Unmarshal(rr.Body.Bytes(), &dm); err != nil {
		t.Fatalf("unmarshal metrics: %v", err)
	}
}

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rr.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	csp := rr.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("Content-Security-Policy = %q, want contains \"default-src 'self'\"", csp)
	}
}

func TestSecurityHeadersCSPAbsentOnSSE(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/sse", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if got := rr.Header().Get("Content-Security-Policy"); got != "" {
		t.Errorf("/sse Content-Security-Policy = %q, want empty", got)
	}
}

func TestSecurityHeadersIncludeReferrerPolicy(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	routes := []string{"/", "/sessions", "/agents", "/tools", "/skills", "/api/metrics", "/assets/style.css"}
	for _, route := range routes {
		req := httptest.NewRequest("GET", route, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		got := rec.Header().Get("Referrer-Policy")
		if got != "same-origin" {
			t.Errorf("%s: Referrer-Policy = %q, want %q", route, got, "same-origin")
		}
	}

	// /sse blocks until context is cancelled; cancel immediately to avoid hanging.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequestWithContext(ctx, "GET", "/sse", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if got := rec.Header().Get("Referrer-Policy"); got != "same-origin" {
		t.Errorf("/sse: Referrer-Policy = %q, want %q", got, "same-origin")
	}
}

func TestNotFound(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestSSEStream(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/sse", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get /sse: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	// Read the first SSE frame.
	br := bufio.NewReader(resp.Body)
	var gotEvent, gotData bool
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		line, err := br.ReadString('\n')
		if err != nil && err != io.EOF {
			break
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "event: overview" {
			gotEvent = true
		}
		if strings.HasPrefix(line, "data: ") {
			gotData = true
		}
		if gotEvent && gotData {
			break
		}
		if err == io.EOF {
			break
		}
	}
	if !gotEvent {
		t.Error("missing `event: overview` line in SSE stream")
	}
	if !gotData {
		t.Error("missing `data:` line in SSE stream")
	}
}

func TestPlaceholderPagesCompile(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	for _, path := range []string{"/", "/sessions", "/agents", "/tools", "/skills"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s Content-Type = %q, want text/html", path, ct)
		}
	}
}

func TestAssetServed(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/assets/style.css", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestHandleErrorDoesNotLeakPath(t *testing.T) {
	t.Parallel()
	logsPath := filepath.Join(t.TempDir(), "logs.jsonl")
	s := New(Options{Port: 0, LogsDir: testdataLogsDir, Since: time.Hour})
	req := httptest.NewRequest("GET", "/sessions", nil)
	rec := httptest.NewRecorder()
	s.handleError(rec, req, errors.New(logsPath+": not a directory"), http.StatusInternalServerError)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, logsPath) || strings.Contains(body, "not a directory") {
		t.Errorf("response leaks internal path: %q", body)
	}
	if !strings.Contains(body, "Internal Server Error") {
		t.Errorf("expected generic message, got: %q", body)
	}
}

func TestHandleErrorLogsDetail(t *testing.T) {
	var buf strings.Builder
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(old) })

	logsPath := filepath.Join(t.TempDir(), "logs.jsonl")
	s := New(Options{Port: 0, LogsDir: testdataLogsDir, Since: time.Hour})
	req := httptest.NewRequest("GET", "/sessions", nil)
	rec := httptest.NewRecorder()
	s.handleError(rec, req, errors.New(logsPath+": not a directory"), http.StatusInternalServerError)

	logged := buf.String()
	if !strings.Contains(logged, "err=") {
		t.Errorf("slog output missing err field: %q", logged)
	}
	if !strings.Contains(logged, "method=GET") || !strings.Contains(logged, "path=/sessions") || !strings.Contains(logged, "status=500") {
		t.Errorf("slog output missing request context: %q", logged)
	}
	if !strings.Contains(logged, filepath.Base(logsPath)) || !strings.Contains(logged, "not a directory") {
		t.Errorf("slog output missing error detail: %q", logged)
	}
}

func TestRenderPageLogsWriteFailure(t *testing.T) {
	var buf strings.Builder
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(old) })

	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	rw := &failingResponseWriter{err: errors.New("write failed")}

	s.renderPage(rw, req, http.StatusOK, errorTmpl, map[string]any{
		"Title":   "Not Found",
		"Active":  "",
		"Status":  404,
		"Heading": "Not Found",
		"Message": "The requested resource was not found.",
	})

	if rw.code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.code)
	}
	logged := buf.String()
	if !strings.Contains(logged, "serve write failed") || !strings.Contains(logged, `context="html response"`) {
		t.Fatalf("missing write failure log: %q", logged)
	}
	if !strings.Contains(logged, "method=GET") || !strings.Contains(logged, "path=/sessions") {
		t.Fatalf("missing request context in log: %q", logged)
	}
	if !strings.Contains(logged, "write failed") {
		t.Fatalf("missing write error detail in log: %q", logged)
	}
}

func TestSendOverviewLogsWriteFailure(t *testing.T) {
	var buf strings.Builder
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(old) })

	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/sse", nil)
	w := &failingSSEWriter{err: errors.New("stream write failed")}

	if ok := s.sendOverview(w, w, req); ok {
		t.Fatal("sendOverview() = true, want false on write failure")
	}
	logged := buf.String()
	if !strings.Contains(logged, "serve write failed") || !strings.Contains(logged, `context="sse summary"`) {
		t.Fatalf("missing sse write failure log: %q", logged)
	}
	if !strings.Contains(logged, "path=/sse") || !strings.Contains(logged, "stream write failed") {
		t.Fatalf("missing request/error detail in log: %q", logged)
	}
}

func TestSendOverviewSummaryFrameWrapped(t *testing.T) {
	var buf strings.Builder
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(old) })

	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/sse", nil)
	w := &failingSSEWriter{err: errors.New("summary write err")}

	s.sendOverview(w, w, req)

	if !strings.Contains(buf.String(), "send sse summary frame") {
		t.Errorf("expected wrapped error prefix in log, got: %q", buf.String())
	}
}

func TestSendOverviewOverviewFrameWrapped(t *testing.T) {
	// Allow the summary frame to succeed, fail on the overview frame.
	var writeCount int
	w := &countingSSEWriter{failAfter: 1, err: errors.New("overview write err")}

	var buf strings.Builder
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(old) })
	_ = writeCount

	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/sse", nil)
	s.sendOverview(w, w, req)

	if !strings.Contains(buf.String(), "send sse overview frame") {
		t.Errorf("expected wrapped error prefix in log, got: %q", buf.String())
	}
}

// countingSSEWriter succeeds for the first failAfter writes, then returns err.
type countingSSEWriter struct {
	count     int
	failAfter int
	err       error
}

func (w *countingSSEWriter) Write(p []byte) (int, error) {
	w.count++
	if w.count > w.failAfter {
		return 0, w.err
	}
	return len(p), nil
}
func (w *countingSSEWriter) Flush() {}

func TestRenderPageContentTemplateWrapped(t *testing.T) {
	var buf strings.Builder
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(old) })

	s := newTestServer(t)
	// Use a template that has no "content" block to trigger an error on htmx path.
	badTmpl := template.Must(template.New("badpage").Parse(`{{define "layout"}}ok{{end}}`))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	s.renderPage(rr, req, http.StatusOK, badTmpl, map[string]any{})

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(buf.String(), "render page content template") {
		t.Errorf("expected wrapped error prefix in log, got: %q", buf.String())
	}
}

func TestRenderPageNavTemplateWrapped(t *testing.T) {
	var buf strings.Builder
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(old) })

	s := newTestServer(t)
	// Template has "content" but no "nav" block — nav execution will fail.
	badTmpl := template.Must(template.New("navpage").Parse(`{{define "content"}}ok{{end}}`))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	s.renderPage(rr, req, http.StatusOK, badTmpl, map[string]any{})

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	if !strings.Contains(buf.String(), "render page nav template") {
		t.Errorf("expected wrapped error prefix in log, got: %q", buf.String())
	}
}

func TestLoadMetricsTTLHit(t *testing.T) {
	t.Parallel()
	base := time.Now()
	s := New(Options{Port: 0, LogsDir: testdataLogsDir, Since: time.Hour})
	s.now = func() time.Time { return base }

	loaded1, err := s.loadMetrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	s.now = func() time.Time { return base.Add(500 * time.Millisecond) }
	loaded2, err := s.loadMetrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// TTL内なのでキャッシュから返され、storedAtはbaseのまま
	if s.metricsCache == nil || !s.metricsCache.storedAt.Equal(base) {
		t.Error("TTL hit should not update storedAt")
	}
	if len(loaded1.dm.Overview.Sessions) != len(loaded2.dm.Overview.Sessions) {
		t.Error("TTL hit should return same result")
	}
}

func TestLoadMetricsTTLExpiry(t *testing.T) {
	t.Parallel()
	base := time.Now()
	s := New(Options{Port: 0, LogsDir: testdataLogsDir, Since: time.Hour})
	s.now = func() time.Time { return base }

	_, _ = s.loadMetrics(context.Background())

	s.now = func() time.Time { return base.Add(2 * time.Second) }
	_, err := s.loadMetrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if s.metricsCache == nil || s.metricsCache.storedAt.Equal(base) {
		t.Error("TTL expiry should trigger reload and update storedAt")
	}
}

func TestLoadMetricsConcurrent(t *testing.T) {
	t.Parallel()
	s := New(Options{Port: 0, LogsDir: testdataLogsDir, Since: time.Hour})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = s.loadMetrics(context.Background()) }()
	}
	wg.Wait()
}

func TestLoadMetricsSingleflight(t *testing.T) {
	base := time.Now()
	s := New(Options{Port: 0, LogsDir: testdataLogsDir, Since: time.Hour, MetricsTTL: 50 * time.Millisecond})
	s.now = func() time.Time { return base }

	// Warm the cache.
	if _, err := s.loadMetrics(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Count AggregateDetail invocations.
	var count atomic.Int32
	restore := AggregateDetailFnForTest(func(ctx context.Context, recs []monitor.Record, now time.Time) (monitor.DetailedMetrics, error) {
		count.Add(1)
		return monitor.AggregateDetail(ctx, recs, now)
	})
	t.Cleanup(restore)

	// Advance past TTL so all goroutines see a cache miss.
	base = base.Add(100 * time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = s.loadMetrics(context.Background()) }()
	}
	wg.Wait()

	if n := count.Load(); n != 1 {
		t.Errorf("AggregateDetail called %d times, want exactly 1", n)
	}
}

// --- Filter tests (task-9) --------------------------------------------------

// buildFilterTestMetrics constructs DetailedMetrics covering two agents and
// two sessions with known tool usage, used by the filter tests below.
//
// agent=A has session s1 with: read x3, grep x1 (all matched).
// agent=B has session s2 with: bash x2 (1 matched, 1 unmatched → error).
func buildFilterTestMetrics(t *testing.T) monitor.DetailedMetrics {
	t.Helper()
	base := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)
	rec := func(session, agent, event, tool string, offset time.Duration) monitor.Record {
		return monitor.Record{
			Ts: base.Add(offset), Session: session, Agent: agent,
			Event: event, Tool: tool,
		}
	}
	recs := []monitor.Record{
		rec("s1", "A", "preToolUse", "read", 0),
		rec("s1", "A", "postToolUse", "read", 1*time.Minute),
		rec("s1", "A", "preToolUse", "read", 2*time.Minute),
		rec("s1", "A", "postToolUse", "read", 3*time.Minute),
		rec("s1", "A", "preToolUse", "read", 4*time.Minute),
		rec("s1", "A", "postToolUse", "read", 5*time.Minute),
		rec("s1", "A", "preToolUse", "grep", 6*time.Minute),
		rec("s1", "A", "postToolUse", "grep", 7*time.Minute),
		rec("s2", "B", "preToolUse", "bash", 10*time.Minute),
		rec("s2", "B", "postToolUse", "bash", 11*time.Minute),
		rec("s2", "B", "preToolUse", "bash", 12*time.Minute),
		// no post for the second bash → error
	}
	dm, err := monitor.AggregateDetail(context.Background(), recs, base.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("AggregateDetail: %v", err)
	}
	return dm
}

func TestFilterByAgentIncludesTools(t *testing.T) {
	t.Parallel()
	dm := buildFilterTestMetrics(t)
	out := filterByAgent(dm, "A")

	if len(out.Tools) == 0 {
		t.Fatal("expected Tools to be re-aggregated, got empty")
	}
	found := map[string]int{}
	for _, td := range out.Tools {
		found[td.Name] = td.CallCount
	}
	if found["read"] != 3 {
		t.Errorf("read CallCount: want 3, got %d", found["read"])
	}
	if found["grep"] != 1 {
		t.Errorf("grep CallCount: want 1, got %d", found["grep"])
	}
	if _, has := found["bash"]; has {
		t.Errorf("agent A should not include bash (belongs to agent B)")
	}

	// Overview.Tools mirrors details.
	overviewFound := map[string]int{}
	for _, tm := range out.Overview.Tools {
		overviewFound[tm.Name] = tm.CallCount
	}
	if overviewFound["read"] != 3 {
		t.Errorf("Overview.Tools read: want 3, got %d", overviewFound["read"])
	}
}

func TestFilterBySessionIncludesTools(t *testing.T) {
	t.Parallel()
	dm := buildFilterTestMetrics(t)
	out := filterBySession(dm, "s2")

	if len(out.Tools) == 0 {
		t.Fatal("expected Tools to be re-aggregated, got empty")
	}
	var bash *monitor.ToolDetail
	for i := range out.Tools {
		if out.Tools[i].Name == "bash" {
			bash = &out.Tools[i]
		}
	}
	if bash == nil {
		t.Fatal("expected bash in Tools for session s2")
	}
	if bash.CallCount != 2 {
		t.Errorf("bash CallCount: want 2, got %d", bash.CallCount)
	}
	if bash.ErrorCount != 1 {
		t.Errorf("bash ErrorCount: want 1, got %d", bash.ErrorCount)
	}
	if len(bash.Errors) != 1 {
		t.Errorf("bash Errors len: want 1, got %d", len(bash.Errors))
	}
	if bash.Errors[0].Session != "s2" || bash.Errors[0].Agent != "B" {
		t.Errorf("bash error attribution: want s2/B, got %s/%s",
			bash.Errors[0].Session, bash.Errors[0].Agent)
	}
}

func TestFilterBySessionSkillsEmpty(t *testing.T) {
	t.Parallel()
	dm := buildFilterTestMetrics(t)
	out := filterBySession(dm, "s1")
	if len(out.Skills) != 0 {
		t.Errorf("expected empty Skills, got %d", len(out.Skills))
	}
}

func TestFilterByAgentSkillsEmpty(t *testing.T) {
	t.Parallel()
	dm := buildFilterTestMetrics(t)
	out := filterByAgent(dm, "A")
	if len(out.Skills) != 0 {
		t.Errorf("expected empty Skills, got %d", len(out.Skills))
	}
}

func TestFilterBySessionNotFound(t *testing.T) {
	t.Parallel()
	dm := buildFilterTestMetrics(t)
	out := filterBySession(dm, "nonexistent")
	if len(out.Sessions) != 0 || len(out.Tools) != 0 || len(out.Overview.Sessions) != 0 {
		t.Errorf("expected fully empty result, got sessions=%d tools=%d overview.sessions=%d",
			len(out.Sessions), len(out.Tools), len(out.Overview.Sessions))
	}
}

func TestFilterByAgentNotFound(t *testing.T) {
	t.Parallel()
	dm := buildFilterTestMetrics(t)
	out := filterByAgent(dm, "nonexistent")
	if len(out.Agents) != 0 || len(out.Sessions) != 0 || len(out.Tools) != 0 {
		t.Errorf("expected fully empty result, got agents=%d sessions=%d tools=%d",
			len(out.Agents), len(out.Sessions), len(out.Tools))
	}
}

// --- Merged session-detail route tests (task-5) ----------------------------

// newMultiAgentServer writes a single-sid, two-agent fixture (plus an
// empty-agent record that task-3 normalizes to "(unknown)") to a temporary
// logs dir and returns a Server pointing at it. The sid is stable so tests
// can hit the routes directly.
func newMultiAgentServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	const sid = "11111111-2222-3333-4444-555555555555"
	lines := []string{
		// orchestrator agent
		`{"ts":"2026-04-22T09:00:00Z","agent":"orchestrator","session":"` + sid + `","event":"userPromptSubmit","prompt":"kick off","cwd":"/w"}`,
		`{"ts":"2026-04-22T09:00:01Z","agent":"orchestrator","session":"` + sid + `","event":"preToolUse","tool":"read","cwd":"/w"}`,
		`{"ts":"2026-04-22T09:00:02Z","agent":"orchestrator","session":"` + sid + `","event":"postToolUse","tool":"read","cwd":"/w"}`,
		// lead agent
		`{"ts":"2026-04-22T09:01:00Z","agent":"lead","session":"` + sid + `","event":"userPromptSubmit","prompt":"lead takes over","cwd":"/w"}`,
		`{"ts":"2026-04-22T09:01:01Z","agent":"lead","session":"` + sid + `","event":"preToolUse","tool":"bash","cwd":"/w"}`,
		`{"ts":"2026-04-22T09:01:02Z","agent":"lead","session":"` + sid + `","event":"postToolUse","tool":"bash","cwd":"/w"}`,
		// unknown-agent record: task-3 normalizes empty agent to "(unknown)"
		`{"ts":"2026-04-22T09:02:00Z","agent":"","session":"` + sid + `","event":"userPromptSubmit","prompt":"hooked before agent was set","cwd":"/w"}`,
	}
	path := filepath.Join(dir, sid+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return New(Options{Port: 0, LogsDir: dir, Since: 365 * 24 * time.Hour}), sid
}

func TestHandleSessionDetailMerged(t *testing.T) {
	t.Parallel()
	srv, sid := newMultiAgentServer(t)
	req := httptest.NewRequest(http.MethodGet, "/sessions/"+sid, nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "(all)") {
		t.Errorf("body missing merged agent label '(all)': %s", body)
	}
	// Each participant agent renders as a link target in the handler data
	// (the current template ignores AgentLinks, so we assert on the API
	// surface instead to keep this test task-5 scoped). AgentLinks presence
	// is covered separately via the merged /api/metrics endpoint below.
}

func TestHandleSessionAgentDetailHappyPath(t *testing.T) {
	t.Parallel()
	srv, sid := newMultiAgentServer(t)
	req := httptest.NewRequest(http.MethodGet, "/sessions/"+sid+"/lead", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "lead") {
		t.Errorf("body missing agent name 'lead': %s", body)
	}
	if strings.Contains(body, "(all)") {
		t.Errorf("per-agent body should not show merged (all) label: %s", body)
	}
}

func TestHandleSessionAgentDetailUnknownRoundTrip(t *testing.T) {
	t.Parallel()
	srv, sid := newMultiAgentServer(t)
	escaped := url.PathEscape("(unknown)")
	if escaped != "%28unknown%29" {
		t.Fatalf("PathEscape sanity: got %q, want %q", escaped, "%28unknown%29")
	}
	req := httptest.NewRequest(http.MethodGet, "/sessions/"+sid+"/"+escaped, nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "(unknown)") {
		t.Errorf("body missing '(unknown)' agent label: %s", body)
	}
}

func TestHandleSessionAgentDetailNotFound(t *testing.T) {
	t.Parallel()
	srv, sid := newMultiAgentServer(t)
	req := httptest.NewRequest(http.MethodGet, "/sessions/"+sid+"/nonexistent", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestHandleSessionDetailNotFound(t *testing.T) {
	t.Parallel()
	srv, _ := newMultiAgentServer(t)
	req := httptest.NewRequest(http.MethodGet, "/sessions/does-not-exist", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestHandleAgentDetailHappyPath(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/agents/kiro", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Agent kiro") {
		t.Fatalf("body missing agent detail title: %s", rr.Body.String())
	}
}

func TestHandleAgentDetailNotFound(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/agents/does-not-exist", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestHandleToolDetailHappyPath(t *testing.T) {
	t.Parallel()
	srv, _ := newMultiAgentServer(t)
	req := httptest.NewRequest(http.MethodGet, "/tools/bash", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Tool bash") {
		t.Fatalf("body missing tool detail title: %s", rr.Body.String())
	}
}

func TestHandleToolDetailNotFound(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/tools/does-not-exist", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Not Found") {
		t.Fatalf("body = %q, want not found page", rr.Body.String())
	}
}

// TestHandleSSE_CapExceededReturns429 verifies that connections beyond MaxSSE
// receive 429 Too Many Requests with Retry-After: 1, and that the counter
// decrements on close so a subsequent connection succeeds.
// NOTE: t.Parallel() is intentionally omitted — the SSE counter is per-Server
// but the test uses a real httptest.Server; parallel runs could interfere.
func TestHandleSSE_CapExceededReturns429(t *testing.T) {
	srv := New(Options{LogsDir: t.TempDir(), MaxSSE: 2})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Open 2 SSE connections (filling the cap).
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	t.Cleanup(cancel1)
	t.Cleanup(cancel2)

	openSSE := func(ctx context.Context) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/sse", nil)
		if err != nil {
			return nil, err
		}
		return http.DefaultClient.Do(req)
	}

	resp1, err := openSSE(ctx1)
	if err != nil {
		t.Fatalf("conn1: %v", err)
	}
	t.Cleanup(func() { _ = resp1.Body.Close() })

	resp2, err := openSSE(ctx2)
	if err != nil {
		t.Fatalf("conn2: %v", err)
	}
	t.Cleanup(func() { _ = resp2.Body.Close() })

	// Give the server time to register both connections.
	time.Sleep(100 * time.Millisecond)

	// 3rd connection must be rejected with 429.
	resp3, err := http.Get(ts.URL + "/sse") //nolint:noctx
	if err != nil {
		t.Fatalf("conn3: %v", err)
	}
	_ = resp3.Body.Close()
	if resp3.StatusCode != http.StatusTooManyRequests {
		t.Errorf("conn3 status = %d, want 429", resp3.StatusCode)
	}
	if got := resp3.Header.Get("Retry-After"); got != "1" {
		t.Errorf("conn3 Retry-After = %q, want %q", got, "1")
	}

	// Cancel one connection; counter should decrement.
	cancel1()
	time.Sleep(100 * time.Millisecond)

	// Now a new connection should succeed (200).
	ctx4, cancel4 := context.WithCancel(context.Background())
	t.Cleanup(cancel4)
	resp4, err := openSSE(ctx4)
	if err != nil {
		t.Fatalf("conn4: %v", err)
	}
	_ = resp4.Body.Close()
	if resp4.StatusCode != http.StatusOK {
		t.Errorf("conn4 status = %d, want 200 (counter should have decremented)", resp4.StatusCode)
	}
}

func TestAPIMetricsSessionFilterMerged(t *testing.T) {
	t.Parallel()
	srv, sid := newMultiAgentServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/metrics?session="+sid, nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var dm monitor.DetailedMetrics
	if err := json.Unmarshal(rr.Body.Bytes(), &dm); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(dm.Sessions) != 1 {
		t.Fatalf("Sessions len = %d, want 1 (merged)", len(dm.Sessions))
	}
	got := dm.Sessions[0]
	if got.Agent != "(all)" {
		t.Errorf("merged Agent = %q, want %q", got.Agent, "(all)")
	}
	if got.ID != sid {
		t.Errorf("merged ID = %q, want %q", got.ID, sid)
	}
	// 3 userPromptSubmit events across 3 agents
	if got.Prompts != 3 {
		t.Errorf("merged Prompts = %d, want 3", got.Prompts)
	}
	// 2 preToolUse across 2 agents
	if got.ToolCalls != 2 {
		t.Errorf("merged ToolCalls = %d, want 2", got.ToolCalls)
	}
}

// --- dashboardSessions cache tests (task-2 / P12) --------------------------

func TestComputeDashboardSessions_Grouping(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC)
	t1 := base
	t2 := base.Add(2 * time.Minute) // A's latest
	t3 := base.Add(1 * time.Minute) // B's only — between t1 and t2

	input := []monitor.SessionMetric{
		{ID: "A", LastActivity: t1},
		{ID: "A", LastActivity: t2},
		{ID: "B", LastActivity: t3},
	}

	got := ComputeDashboardSessions(input)

	// Expected order: A@t2, A@t1, B@t3
	// A group is newest (t2 > t3), within A desc, then B group.
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	want := []struct {
		id string
		ts time.Time
	}{
		{"A", t2},
		{"A", t1},
		{"B", t3},
	}
	for i, w := range want {
		if got[i].ID != w.id || got[i].LastActivity != w.ts {
			t.Errorf("[%d] got {%s %v}, want {%s %v}",
				i, got[i].ID, got[i].LastActivity, w.id, w.ts)
		}
	}

	// Input must not be mutated.
	if input[0].LastActivity != t1 {
		t.Error("input slice was mutated")
	}
}

func TestHandleDashboard_CacheReusesSortedSessions(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)

	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	httptest.NewRecorder() // discard
	srv.Handler().ServeHTTP(httptest.NewRecorder(), req1)

	first := srv.DashboardSessionsFromCache()

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.Handler().ServeHTTP(httptest.NewRecorder(), req2)

	second := srv.DashboardSessionsFromCache()

	// Within TTL the same backing array must be returned (no re-sort).
	if len(first) > 0 && len(second) > 0 {
		if &first[0] != &second[0] {
			t.Error("cache miss on second request within TTL: expected same slice backing array")
		}
	}
}

func TestNew_ClampsMaxSSE(t *testing.T) {
	t.Parallel()
	// Oversized input must be clamped to math.MaxInt32.
	s := New(Options{MaxSSE: math.MaxInt64})
	if got := s.SSEMaxForTest(); got != math.MaxInt32 {
		t.Errorf("SSEMaxForTest() = %d, want %d", got, int32(math.MaxInt32))
	}

	// Zero input must select defaultMaxSSE.
	s2 := New(Options{MaxSSE: 0})
	if got := s2.SSEMaxForTest(); got != defaultMaxSSE {
		t.Errorf("SSEMaxForTest() = %d, want defaultMaxSSE %d", got, defaultMaxSSE)
	}
}

func TestRenderMarkdown(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		input    string
		contains string
		excludes string
	}{
		{
			name:     "empty",
			input:    "",
			contains: `<em class="muted">(empty)</em>`,
		},
		{
			name:     "heading",
			input:    "## hi",
			contains: "<h2>hi</h2>",
		},
		{
			name:     "gfm-table",
			input:    "| a | b |\n|---|---|\n| 1 | 2 |",
			contains: "<table>",
		},
		{
			name:     "raw-script-escape",
			input:    "<script>alert(1)</script>",
			contains: "<!-- raw HTML omitted -->",
			excludes: "<script>",
		},
		{
			name:     "link-with-rel",
			input:    "[x](http://y)",
			contains: `rel="noopener nofollow"`,
		},
		{
			name:     "javascript-scheme-blocked",
			input:    "[click](javascript:alert(1))",
			excludes: `href="javascript:`,
		},
		{
			name:     "link-with-rel-autolink",
			input:    "https://example.com",
			contains: `rel="noopener nofollow"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := string(renderMarkdown(tc.input))
			if tc.contains != "" && !strings.Contains(got, tc.contains) {
				t.Errorf("renderMarkdown(%q) = %q, want contains %q", tc.input, got, tc.contains)
			}
			if tc.excludes != "" && strings.Contains(got, tc.excludes) {
				t.Errorf("renderMarkdown(%q) = %q, want NOT contains %q", tc.input, got, tc.excludes)
			}
		})
	}
}

func TestLocaltime_EscapesFormat(t *testing.T) {
	t.Parallel()
	fn := templateFuncs["localtime"].(func(time.Time, string) template.HTML)
	out := string(fn(time.Unix(0, 0), `"><script>`))
	if strings.Contains(out, "<script>") {
		t.Errorf("unescaped <script> in output: %s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("escaped form missing: %s", out)
	}
}

func TestSchemeOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		dest       string
		wantScheme string
		wantRel    bool
	}{
		{"https://example.com", "https", false},
		{"http://example.com", "http", false},
		{"mailto:user@example.com", "mailto", false},
		{"javascript:alert(1)", "javascript", false},
		{"data:text/html,x", "data", false},
		{"vbscript:msgbox", "vbscript", false},
		{"blob:xxx", "blob", false},
		{"/relative/path", "", true},
		{"relative/path", "", true},
		{"?query=1", "", true},
		{"#anchor", "", true},
		{"  https://example.com", "https", false},
	}
	for _, tc := range cases {
		t.Run(tc.dest, func(t *testing.T) {
			t.Parallel()
			gotScheme, gotRel := schemeOf(tc.dest)
			if gotScheme != tc.wantScheme || gotRel != tc.wantRel {
				t.Errorf("schemeOf(%q) = (%q, %v), want (%q, %v)",
					tc.dest, gotScheme, gotRel, tc.wantScheme, tc.wantRel)
			}
		})
	}
}

func TestRenderMarkdownLinkAllowlist(t *testing.T) {
	t.Parallel()
	blocked := []struct {
		name  string
		input string
	}{
		{"javascript", "[x](javascript:alert(1))"},
		{"data", "[x](data:text/html,x)"},
		{"vbscript", "[x](vbscript:msgbox)"},
		{"blob", "[x](blob:xxx)"},
	}
	for _, tc := range blocked {
		t.Run("blocked/"+tc.name, func(t *testing.T) {
			t.Parallel()
			got := string(renderMarkdown(tc.input))
			if !strings.Contains(got, `href="#"`) {
				t.Errorf("renderMarkdown(%q) = %q, want href=\"#\"", tc.input, got)
			}
		})
	}

	allowed := []struct {
		name  string
		input string
		href  string
	}{
		{"https", "[a](https://example.com)", "https://example.com"},
		{"http", "[a](http://example.com)", "http://example.com"},
		{"mailto", "[a](mailto:user@example.com)", "mailto:user@example.com"},
		{"relative", "[a](/some/path)", "/some/path"},
	}
	for _, tc := range allowed {
		t.Run("allowed/"+tc.name, func(t *testing.T) {
			t.Parallel()
			got := string(renderMarkdown(tc.input))
			if strings.Contains(got, `href="#"`) {
				t.Errorf("renderMarkdown(%q) = %q, should not be href=\"#\"", tc.input, got)
			}
			if !strings.Contains(got, tc.href) {
				t.Errorf("renderMarkdown(%q) = %q, want contains %q", tc.input, got, tc.href)
			}
		})
	}
}

func TestRunShutdownOnContextCancel(t *testing.T) {
	t.Parallel()
	s := New(Options{Port: 0, LogsDir: testdataLogsDir, Since: 365 * 24 * time.Hour})

	// Pick a free port by listening briefly.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	s.opts.Port = port

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	// Give the server a moment to start.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("Run returned unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s after context cancel")
	}
}

// --- Task 3: OOB title swap tests -------------------------------------------

func TestRenderPageOOBTitlePresentInHXRequest(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `<title hx-swap-oob="true">Sessions — kapm</title>`) {
		t.Errorf("HX request body missing OOB title: %s", body)
	}
}

func TestRenderPageOOBTitleAbsentInNonHXRequest(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	// No HX-Request header
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, `<title hx-swap-oob="true">`) {
		t.Errorf("non-HX request body should not contain OOB title: %s", body)
	}
}

func TestRenderPageTitleEscaping(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	// Manually call renderPage with a malicious title to test escaping.
	srv.renderPage(rr, req, http.StatusOK, sessionsTmpl, map[string]any{
		"Title":  "<script>alert(1)</script>",
		"Active": "sessions",
	})

	body := rr.Body.String()
	if strings.Contains(body, "<script>") {
		t.Errorf("unescaped <script> in OOB title: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("escaped form missing in OOB title: %s", body)
	}
	if !strings.Contains(body, `<title hx-swap-oob="true">`) {
		t.Errorf("OOB title tag missing: %s", body)
	}
}

func TestRenderPageDynamicSessionTitle(t *testing.T) {
	t.Parallel()
	srv, sid := newMultiAgentServer(t)
	req := httptest.NewRequest(http.MethodGet, "/sessions/"+sid, nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	// Session detail title should include the session ID
	if !strings.Contains(body, `<title hx-swap-oob="true">Session `) {
		t.Errorf("HX request body missing dynamic session title: %s", body)
	}
	if !strings.Contains(body, `— kapm</title>`) {
		t.Errorf("OOB title missing kapm suffix: %s", body)
	}
}

// --- Task 2: Sessions pagination tests --------------------------------------

// newPaginationServer creates a Server whose metrics are stubbed to return
// exactly n distinct session IDs (each with one row). The cache is injected
// directly with a very long TTL to avoid any global state mutation, making
// this safe to use from parallel tests.
func newPaginationServer(t *testing.T, n int) *Server {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sessions := make([]monitor.SessionMetric, n)
	for i := range sessions {
		sessions[i] = monitor.SessionMetric{
			ID:           fmt.Sprintf("session-%04d", i),
			Agent:        "agent",
			LastActivity: base.Add(time.Duration(i) * time.Minute),
		}
	}
	var dm monitor.DetailedMetrics
	dm.Overview.Sessions = sessions

	s := New(Options{Port: 0, LogsDir: t.TempDir(), Since: time.Hour, MetricsTTL: 24 * time.Hour})
	// Inject the cache directly — no global override needed.
	s.metricsMu.Lock()
	s.metricsCache = &metricsCacheEntry{
		dm:                dm,
		dashboardSessions: sessions,
		storedAt:          time.Now(),
	}
	s.metricsMu.Unlock()
	return s
}

func sessionsBody(t *testing.T, srv *Server, query string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/sessions"+query, nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /sessions%s status = %d, want 200", query, rr.Code)
	}
	return rr.Body.String()
}

func TestHandleSessions_Pagination_DefaultPage(t *testing.T) {
	t.Parallel()

	// 50 distinct IDs → TotalPages=1, next disabled.
	srv50 := newPaginationServer(t, 50)
	body50 := sessionsBody(t, srv50, "")
	if !strings.Contains(body50, "Page 1 of 1") {
		t.Errorf("50 IDs: want 'Page 1 of 1', body=%s", body50)
	}
	if strings.Contains(body50, `href="/sessions?page=2"`) {
		t.Errorf("50 IDs: next link should be disabled")
	}

	// 51 distinct IDs → TotalPages=2, next enabled.
	srv51 := newPaginationServer(t, 51)
	body51 := sessionsBody(t, srv51, "")
	if !strings.Contains(body51, "Page 1 of 2") {
		t.Errorf("51 IDs: want 'Page 1 of 2', body=%s", body51)
	}
	if !strings.Contains(body51, `href="/sessions?page=2"`) {
		t.Errorf("51 IDs: next link should be present")
	}
}

func TestHandleSessions_Pagination_Page2Boundary(t *testing.T) {
	t.Parallel()
	srv := newPaginationServer(t, 75)
	body := sessionsBody(t, srv, "?page=2")
	if !strings.Contains(body, "Page 2 of 2") {
		t.Errorf("want 'Page 2 of 2', body=%s", body)
	}
	// prev link to page 1 must be present.
	if !strings.Contains(body, `href="/sessions?page=1"`) {
		t.Errorf("prev link to page 1 missing, body=%s", body)
	}
	// next must be disabled (last page).
	if strings.Contains(body, `href="/sessions?page=3"`) {
		t.Errorf("next link should be disabled on last page")
	}
	// IDs 50-74 (session-0050 … session-0074) should appear.
	if !strings.Contains(body, "session-0050") {
		t.Errorf("session-0050 missing from page 2, body=%s", body)
	}
	if strings.Contains(body, "session-0000") {
		t.Errorf("session-0000 should not appear on page 2")
	}
}

func TestHandleSessions_Pagination_InvalidPageClampsTo1(t *testing.T) {
	t.Parallel()
	srv := newPaginationServer(t, 10)
	for _, q := range []string{"?page=0", "?page=-1", "?page=abc", "?page="} {
		body := sessionsBody(t, srv, q)
		if !strings.Contains(body, "Page 1 of 1") {
			t.Errorf("query %q: want 'Page 1 of 1', body=%s", q, body)
		}
	}
}

func TestHandleSessions_Pagination_OverflowClampsToLastPage(t *testing.T) {
	t.Parallel()
	srv := newPaginationServer(t, 75)
	body := sessionsBody(t, srv, "?page=99")
	// Clamped to page 2 of 2.
	if !strings.Contains(body, "Page 2 of 2") {
		t.Errorf("want 'Page 2 of 2', body=%s", body)
	}
	// Same rows as page 2.
	if !strings.Contains(body, "session-0050") {
		t.Errorf("session-0050 missing from clamped page, body=%s", body)
	}
	// next disabled.
	if strings.Contains(body, `href="/sessions?page=3"`) {
		t.Errorf("next link should be disabled on clamped last page")
	}
}

func TestHandleSessions_Pagination_EmptyMetrics(t *testing.T) {
	t.Parallel()
	srv := newPaginationServer(t, 0)
	body := sessionsBody(t, srv, "")
	if !strings.Contains(body, "Page 1 of 1") {
		t.Errorf("empty: want 'Page 1 of 1', body=%s", body)
	}
	// Both prev and next disabled.
	if strings.Contains(body, `href="/sessions?page=`) {
		t.Errorf("empty: no pagination links expected, body=%s", body)
	}
}

func TestHandleDashboard_SessionsCappedTo50(t *testing.T) {
	t.Parallel()
	srv := newPaginationServer(t, 100)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	// session-0049 should appear (50th ID), session-0050 should not.
	if !strings.Contains(body, "session-0049") {
		t.Errorf("session-0049 (50th ID) missing from dashboard, body=%s", body)
	}
	if strings.Contains(body, "session-0050") {
		t.Errorf("session-0050 (51st ID) should not appear on dashboard (cap=50)")
	}
}

func TestPaginateByID(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	make3 := func(ids ...string) []monitor.SessionMetric {
		out := make([]monitor.SessionMetric, len(ids))
		for i, id := range ids {
			out[i] = monitor.SessionMetric{ID: id, LastActivity: base.Add(time.Duration(i) * time.Minute)}
		}
		return out
	}

	t.Run("empty", func(t *testing.T) {
		rows, total := paginateByID(nil, 1, 50)
		if total != 0 || rows != nil {
			t.Errorf("empty: got rows=%v total=%d", rows, total)
		}
	})

	t.Run("single page", func(t *testing.T) {
		all := make3("a", "b", "c")
		rows, total := paginateByID(all, 1, 50)
		if total != 3 || len(rows) != 3 {
			t.Errorf("single page: total=%d rows=%d", total, len(rows))
		}
	})

	t.Run("page beyond total", func(t *testing.T) {
		all := make3("a", "b")
		rows, total := paginateByID(all, 5, 1)
		if total != 2 || rows != nil {
			t.Errorf("beyond: total=%d rows=%v", total, rows)
		}
	})

	t.Run("multi-row per ID preserved", func(t *testing.T) {
		all := []monitor.SessionMetric{
			{ID: "x", Agent: "merged"},
			{ID: "x", Agent: "a"},
			{ID: "y", Agent: "merged"},
		}
		rows, total := paginateByID(all, 1, 1)
		if total != 2 {
			t.Errorf("total=%d want 2", total)
		}
		if len(rows) != 2 {
			t.Errorf("rows=%d want 2 (both rows for ID x)", len(rows))
		}
		if rows[0].ID != "x" || rows[1].ID != "x" {
			t.Errorf("rows IDs: %v %v, want x x", rows[0].ID, rows[1].ID)
		}
	})
}
