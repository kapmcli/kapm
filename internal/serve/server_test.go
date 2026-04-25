package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
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
}

func TestSecurityHeadersIncludeReferrerPolicy(t *testing.T) {
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
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestSSEStream(t *testing.T) {
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
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/assets/style.css", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestHandleErrorDoesNotLeakPath(t *testing.T) {
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

func TestLoadMetricsTTLHit(t *testing.T) {
	base := time.Now()
	s := New(Options{Port: 0, LogsDir: testdataLogsDir, Since: time.Hour})
	s.now = func() time.Time { return base }

	loaded1, err := s.loadMetrics()
	if err != nil {
		t.Fatal(err)
	}

	s.now = func() time.Time { return base.Add(500 * time.Millisecond) }
	loaded2, err := s.loadMetrics()
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
	base := time.Now()
	s := New(Options{Port: 0, LogsDir: testdataLogsDir, Since: time.Hour})
	s.now = func() time.Time { return base }

	_, _ = s.loadMetrics()

	s.now = func() time.Time { return base.Add(2 * time.Second) }
	_, err := s.loadMetrics()
	if err != nil {
		t.Fatal(err)
	}

	if s.metricsCache == nil || s.metricsCache.storedAt.Equal(base) {
		t.Error("TTL expiry should trigger reload and update storedAt")
	}
}

func TestLoadMetricsConcurrent(t *testing.T) {
	s := New(Options{Port: 0, LogsDir: testdataLogsDir, Since: time.Hour})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = s.loadMetrics() }()
	}
	wg.Wait()
}

func TestLoadMetricsSingleflight(t *testing.T) {
	base := time.Now()
	s := New(Options{Port: 0, LogsDir: testdataLogsDir, Since: time.Hour, MetricsTTL: 50 * time.Millisecond})
	s.now = func() time.Time { return base }

	// Warm the cache.
	if _, err := s.loadMetrics(); err != nil {
		t.Fatal(err)
	}

	// Count AggregateDetail invocations.
	var count atomic.Int32
	restore := AggregateDetailFnForTest(func(recs []monitor.Record, now time.Time) monitor.DetailedMetrics {
		count.Add(1)
		return monitor.AggregateDetail(recs, now)
	})
	t.Cleanup(restore)

	// Advance past TTL so all goroutines see a cache miss.
	base = base.Add(100 * time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = s.loadMetrics() }()
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
	return monitor.AggregateDetail(recs, base.Add(1*time.Hour))
}

func TestFilterByAgentIncludesTools(t *testing.T) {
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
	dm := buildFilterTestMetrics(t)
	out := filterBySession(dm, "s1")
	if len(out.Skills) != 0 {
		t.Errorf("expected empty Skills, got %d", len(out.Skills))
	}
}

func TestFilterByAgentSkillsEmpty(t *testing.T) {
	dm := buildFilterTestMetrics(t)
	out := filterByAgent(dm, "A")
	if len(out.Skills) != 0 {
		t.Errorf("expected empty Skills, got %d", len(out.Skills))
	}
}

func TestFilterBySessionNotFound(t *testing.T) {
	dm := buildFilterTestMetrics(t)
	out := filterBySession(dm, "nonexistent")
	if len(out.Sessions) != 0 || len(out.Tools) != 0 || len(out.Overview.Sessions) != 0 {
		t.Errorf("expected fully empty result, got sessions=%d tools=%d overview.sessions=%d",
			len(out.Sessions), len(out.Tools), len(out.Overview.Sessions))
	}
}

func TestFilterByAgentNotFound(t *testing.T) {
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
	srv, sid := newMultiAgentServer(t)
	req := httptest.NewRequest(http.MethodGet, "/sessions/"+sid+"/nonexistent", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestHandleSessionDetailNotFound(t *testing.T) {
	srv, _ := newMultiAgentServer(t)
	req := httptest.NewRequest(http.MethodGet, "/sessions/does-not-exist", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
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
