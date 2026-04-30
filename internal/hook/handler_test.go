package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/testutil"
)

var fixedNow = func() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	var lines []string
	for _, l := range strings.Split(string(data), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		t.Fatalf("no log lines in %q", path)
	}
	return lines
}

func logFile(dir, session string) string {
	return filepath.Join(dir, ".kapm", "logs", session+".jsonl")
}

func TestHandleWritesPreToolUseRecord(t *testing.T) {
	dir := t.TempDir()
	in := strings.NewReader(`{"hook_event_name":"preToolUse","session_id":"s1","cwd":"/tmp","tool_name":"read"}`)
	code := Handle(in, &bytes.Buffer{}, &bytes.Buffer{}, fixedNow, dir, "myagent")
	if code != 0 {
		t.Fatalf("want 0, got %d", code)
	}
	lines := readLines(t, logFile(dir, "s1"))
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(lines))
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec["event"] != "preToolUse" {
		t.Errorf("event: got %v", rec["event"])
	}
	if rec["agent"] != "myagent" {
		t.Errorf("agent: got %v", rec["agent"])
	}
	if rec["session"] != "s1" {
		t.Errorf("session: got %v", rec["session"])
	}
}

func TestHandleWritesToolEventTypes(t *testing.T) {
	events := []struct {
		name  string
		input string
	}{
		{"preToolUse", `{"hook_event_name":"preToolUse","session_id":"s2","cwd":"/tmp","tool_name":"fs_read","tool_input":{"path":"/x"}}`},
		{"postToolUse", `{"hook_event_name":"postToolUse","session_id":"s2","cwd":"/tmp","tool_name":"fs_write","tool_input":{"path":"/y"},"tool_response":{"ok":true}}`},
		{"agentSpawn", `{"hook_event_name":"agentSpawn","session_id":"s2","cwd":"/tmp"}`},
		{"stop", `{"hook_event_name":"stop","session_id":"s2","cwd":"/tmp"}`},
	}
	dir := t.TempDir()
	// userPromptSubmit should write nothing
	Handle(strings.NewReader(`{"hook_event_name":"userPromptSubmit","session_id":"s2","cwd":"/tmp","prompt":"hi"}`), &bytes.Buffer{}, &bytes.Buffer{}, fixedNow, dir, "a")

	for _, ev := range events {
		code := Handle(strings.NewReader(ev.input), &bytes.Buffer{}, &bytes.Buffer{}, fixedNow, dir, "a")
		if code != 0 {
			t.Fatalf("%s: want 0, got %d", ev.name, code)
		}
	}
	lines := readLines(t, logFile(dir, "s2"))
	if len(lines) != 4 {
		t.Fatalf("want 4 lines (pre/postToolUse + agentSpawn + stop), got %d", len(lines))
	}
	for i, ev := range events {
		var rec map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &rec); err != nil {
			t.Fatalf("line %d unmarshal: %v", i, err)
		}
		if rec["event"] != ev.name {
			t.Errorf("line %d: event want %q got %v", i, ev.name, rec["event"])
		}
	}
}

func TestHandleOmitsMissingFields(t *testing.T) {
	dir := t.TempDir()
	in := strings.NewReader(`{"hook_event_name":"preToolUse","session_id":"s3","cwd":"/tmp"}`)
	Handle(in, &bytes.Buffer{}, &bytes.Buffer{}, fixedNow, dir, "a")
	lines := readLines(t, logFile(dir, "s3"))
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := rec["tool"]; ok {
		t.Error("tool field should be omitted")
	}
	if _, ok := rec["prompt"]; ok {
		t.Error("prompt field should be omitted")
	}
}

func TestHandleFallsBackWhenSessionIdEmpty(t *testing.T) {
	dir := t.TempDir()
	var nano int64 = 1234567890
	now := func() time.Time { return time.Unix(0, nano) }
	in := strings.NewReader(`{"hook_event_name":"preToolUse","cwd":"/tmp","tool_name":"read"}`)
	var stderr bytes.Buffer
	code := Handle(in, &bytes.Buffer{}, &stderr, now, dir, "a")
	if code != 0 {
		t.Fatalf("want 0, got %d", code)
	}
	expected := fmt.Sprintf("unknown-%d.jsonl", nano)
	entries, _ := os.ReadDir(filepath.Join(dir, ".kapm", "logs"))
	if len(entries) != 1 || entries[0].Name() != expected {
		t.Errorf("want file %q, got %v", expected, entries)
	}
	if !strings.Contains(stderr.String(), "invalid session_id") {
		t.Errorf("want stderr message, got %q", stderr.String())
	}
}

func TestHandleNeverWritesStdout(t *testing.T) {
	dir := t.TempDir()
	var stdout bytes.Buffer
	in := strings.NewReader(`{"hook_event_name":"preToolUse","session_id":"s4","cwd":"/tmp","tool_name":"read"}`)
	Handle(in, &stdout, &bytes.Buffer{}, fixedNow, dir, "a")
	if stdout.Len() != 0 {
		t.Errorf("stdout should be empty, got %q", stdout.String())
	}
}

func TestHandleAgentFlagWinsOverEnv(t *testing.T) {
	// The Handle function receives agent as a parameter; the caller (main.go) resolves
	// flag vs env. This test verifies that when agent="bar" is passed, it wins.
	dir := t.TempDir()
	in := strings.NewReader(`{"hook_event_name":"preToolUse","session_id":"s5","cwd":"/tmp","tool_name":"read"}`)
	Handle(in, &bytes.Buffer{}, &bytes.Buffer{}, fixedNow, dir, "bar")
	lines := readLines(t, logFile(dir, "s5"))
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec["agent"] != "bar" {
		t.Errorf("agent: want bar, got %v", rec["agent"])
	}
}

func TestHandleSurvivesPanicInRecord(t *testing.T) {
	// Inject a now() that panics to simulate an unexpected panic.
	dir := t.TempDir()
	in := strings.NewReader(`{"hook_event_name":"preToolUse","session_id":"s6","cwd":"/tmp","tool_name":"read"}`)
	panicNow := func() time.Time { panic("test panic") }
	var stderr bytes.Buffer
	code := Handle(in, &bytes.Buffer{}, &stderr, panicNow, dir, "a")
	if code != 0 {
		t.Fatalf("want 0 after panic, got %d", code)
	}
}

func TestHandleInvalidSessionIDFallsBack(t *testing.T) {
	invalid := []string{
		"../../etc/passwd",
		"/abs/path",
		"a/b",
		`a\b`,
		"..",
		".",
		"",
		"foo.bar",
	}
	for _, id := range invalid {
		t.Run(id, func(t *testing.T) {
			dir := t.TempDir()
			var nano int64 = 999
			now := func() time.Time { return time.Unix(0, nano) }
			input := fmt.Sprintf(`{"hook_event_name":"preToolUse","session_id":%q,"tool_name":"t"}`, id)
			var stderr bytes.Buffer
			code := Handle(strings.NewReader(input), &bytes.Buffer{}, &stderr, now, dir, "a")
			if code != 0 {
				t.Fatalf("want 0, got %d", code)
			}
			expected := fmt.Sprintf("unknown-%d.jsonl", nano)
			entries, _ := os.ReadDir(filepath.Join(dir, ".kapm", "logs"))
			if len(entries) != 1 || entries[0].Name() != expected {
				t.Errorf("id=%q: want file %q, got %v", id, expected, entries)
			}
			if !strings.Contains(stderr.String(), "hook-handler: invalid session_id") {
				t.Errorf("id=%q: want invalid session_id in stderr, got %q", id, stderr.String())
			}
		})
	}
}

func TestHandleConcurrentAppendPreservesAllLines(t *testing.T) {
	dir := t.TempDir()
	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			in := strings.NewReader(`{"hook_event_name":"preToolUse","session_id":"conc","cwd":"/tmp","tool_name":"fs_read","tool_input":{"data":"` + strings.Repeat("x", 10000) + `"}}`)
			Handle(in, &bytes.Buffer{}, &bytes.Buffer{}, fixedNow, dir, "a")
		}()
	}
	wg.Wait()

	lines := readLines(t, logFile(dir, "conc"))
	if len(lines) != n {
		t.Fatalf("want %d lines, got %d", n, len(lines))
	}
	for i, l := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(l), &rec); err != nil {
			t.Errorf("line %d corrupt: %v", i, err)
		}
	}
}

func TestHandleRejectsOversizedInput(t *testing.T) {
	dir := t.TempDir()
	in := strings.NewReader(strings.Repeat("x", 11<<20))
	var stderr bytes.Buffer
	code := Handle(in, &bytes.Buffer{}, &stderr, fixedNow, dir, "a")
	if code != 0 {
		t.Fatalf("want 0, got %d", code)
	}
	if !strings.Contains(stderr.String(), "too large") {
		t.Errorf("want 'too large' in stderr, got %q", stderr.String())
	}
}

func TestHandleOversizedEvent_Warns(t *testing.T) {
	buf, restore := testutil.CaptureSlog(t)
	defer restore()

	dir := t.TempDir()
	in := strings.NewReader(strings.Repeat("x", 11<<20))
	code := Handle(in, &bytes.Buffer{}, &bytes.Buffer{}, fixedNow, dir, "a")
	if code != 0 {
		t.Fatalf("want 0, got %d", code)
	}
	if !strings.Contains(buf.String(), "hook event rejected") {
		t.Errorf("expected slog warn, got: %s", buf.String())
	}
}

func TestHandleRejectsSymlinkedKapm(t *testing.T) {
	dir := t.TempDir()
	target := t.TempDir()
	kapmLink := filepath.Join(dir, ".kapm")
	if err := os.Symlink(target, kapmLink); err != nil {
		t.Skipf("os.Symlink not available: %v", err)
	}
	in := strings.NewReader(`{"hook_event_name":"preToolUse","session_id":"s-sym","cwd":"/tmp","tool_name":"read"}`)
	var stderr bytes.Buffer
	code := Handle(in, &bytes.Buffer{}, &stderr, fixedNow, dir, "a")
	if code != 0 {
		t.Fatalf("want 0, got %d", code)
	}
	if !strings.Contains(stderr.String(), "is a symlink") {
		t.Errorf("want symlink warning in stderr, got %q", stderr.String())
	}
	// log file must not exist
	if _, err := os.Stat(filepath.Join(target, "logs")); err == nil {
		t.Error("log dir should not have been created under symlink target")
	}
}

func TestHandleInvalidAgent(t *testing.T) {
	dir := t.TempDir()
	in := strings.NewReader(`{"hook_event_name":"preToolUse","session_id":"s-inv","cwd":"/tmp","tool_name":"read"}`)
	var stderr bytes.Buffer
	code := Handle(in, &bytes.Buffer{}, &stderr, fixedNow, dir, "bad/agent")
	if code != 0 {
		t.Fatalf("want 0, got %d", code)
	}
	if !strings.Contains(stderr.String(), "invalid agent name") {
		t.Errorf("want invalid agent warning in stderr, got %q", stderr.String())
	}
	lines := readLines(t, logFile(dir, "s-inv"))
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := rec["agent"]; ok {
		t.Error("agent field should be omitted when cleared")
	}
}

func TestHandleJSONLRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := strings.NewReader(`{"hook_event_name":"preToolUse","session_id":"s-rt","cwd":"/proj","tool_name":"fs_read","tool_input":{"path":"/x"}}`)
	code := Handle(in, &bytes.Buffer{}, &bytes.Buffer{}, fixedNow, dir, "myagent")
	if code != 0 {
		t.Fatalf("want 0, got %d", code)
	}
	logsDir := filepath.Join(dir, ".kapm", "logs")
	lines := readLines(t, filepath.Join(logsDir, "s-rt.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("want 1 record, got %d", len(lines))
	}
	var r struct {
		Event string `json:"event"`
		Tool  string `json:"tool"`
		Agent string `json:"agent"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Event != "preToolUse" {
		t.Errorf("event: got %q", r.Event)
	}
	if r.Tool != "fs_read" {
		t.Errorf("tool: got %q", r.Tool)
	}
	if r.Agent != "myagent" {
		t.Errorf("agent: got %q", r.Agent)
	}
}

func TestHandleMinimalRecord(t *testing.T) {
	dir := t.TempDir()
	in := strings.NewReader(`{"hook_event_name":"preToolUse","session_id":"s-min","tool_name":"fs_read","tool_input":{"path":"/x"},"cwd":"/proj","prompt":"hi","assistant_response":{"text":"ok"}}`)
	var stderr bytes.Buffer
	Handle(in, &bytes.Buffer{}, &stderr, fixedNow, dir, "a")
	if stderr.Len() > 0 {
		t.Fatalf("Handle wrote to stderr: %s", stderr.String())
	}
	lines := readLines(t, logFile(dir, "s-min"))
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, banned := range []string{"tool_input", "tool_response", "assistant_response", "prompt", "cwd"} {
		if _, ok := rec[banned]; ok {
			t.Errorf("field %q must not appear in record", banned)
		}
	}
	for _, required := range []string{"ts", "session", "event", "agent", "tool"} {
		if _, ok := rec[required]; !ok {
			t.Errorf("field %q must appear in record", required)
		}
	}
}

func TestHandleUserPromptSubmitWritesNothing(t *testing.T) {
	dir := t.TempDir()
	in := strings.NewReader(`{"hook_event_name":"userPromptSubmit","session_id":"s-ups","prompt":"hello"}`)
	code := Handle(in, &bytes.Buffer{}, &bytes.Buffer{}, fixedNow, dir, "a")
	if code != 0 {
		t.Fatalf("want 0, got %d", code)
	}
	logPath := logFile(dir, "s-ups")
	if _, err := os.Stat(logPath); err == nil {
		t.Error("log file must not be created for userPromptSubmit")
	}
}

func TestHandleAgentSpawnWritesRecord(t *testing.T) {
	dir := t.TempDir()
	in := strings.NewReader(`{"hook_event_name":"agentSpawn","session_id":"s-spawn","cwd":"/tmp"}`)
	code := Handle(in, &bytes.Buffer{}, &bytes.Buffer{}, fixedNow, dir, "myagent")
	if code != 0 {
		t.Fatalf("want 0, got %d", code)
	}
	lines := readLines(t, logFile(dir, "s-spawn"))
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(lines))
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec["event"] != "agentSpawn" {
		t.Errorf("event: got %v", rec["event"])
	}
	if rec["agent"] != "myagent" {
		t.Errorf("agent: got %v", rec["agent"])
	}
}

func TestHandleStopWritesRecord(t *testing.T) {
	dir := t.TempDir()
	in := strings.NewReader(`{"hook_event_name":"stop","session_id":"s-stop","cwd":"/tmp"}`)
	code := Handle(in, &bytes.Buffer{}, &bytes.Buffer{}, fixedNow, dir, "myagent")
	if code != 0 {
		t.Fatalf("want 0, got %d", code)
	}
	lines := readLines(t, logFile(dir, "s-stop"))
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(lines))
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec["event"] != "stop" {
		t.Errorf("event: got %v", rec["event"])
	}
	if rec["agent"] != "myagent" {
		t.Errorf("agent: got %v", rec["agent"])
	}
}

func TestHandleShellExitStatus(t *testing.T) {
	dir := t.TempDir()
	payload := `{"hook_event_name":"postToolUse","session_id":"s-sh","tool_name":"shell","tool_response":{"items":[{"Json":{"exit_status":"exit status: 1","stdout":"","stderr":""}}]}}`
	Handle(strings.NewReader(payload), &bytes.Buffer{}, &bytes.Buffer{}, fixedNow, dir, "a")
	lines := readLines(t, logFile(dir, "s-sh"))
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec["shell_exit_status"] != "exit status: 1" {
		t.Errorf("shell_exit_status: got %v", rec["shell_exit_status"])
	}
}

func TestHandlePostToolUseNonShellNoExitStatus(t *testing.T) {
	dir := t.TempDir()
	payload := `{"hook_event_name":"postToolUse","session_id":"s-nsh","tool_name":"fs_read","tool_response":{"items":[{"Json":{"exit_status":"exit status: 0"}}]}}`
	Handle(strings.NewReader(payload), &bytes.Buffer{}, &bytes.Buffer{}, fixedNow, dir, "a")
	lines := readLines(t, logFile(dir, "s-nsh"))
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := rec["shell_exit_status"]; ok {
		t.Error("shell_exit_status must not appear for non-shell tool")
	}
}

func TestHandleParseJSONStderrFormat(t *testing.T) {
	dir := t.TempDir()
	var stderr bytes.Buffer
	code := Handle(strings.NewReader("not-json"), &bytes.Buffer{}, &stderr, fixedNow, dir, "a")
	if code != 0 {
		t.Fatalf("want 0, got %d", code)
	}
	got := stderr.String()
	if !strings.HasPrefix(got, "hook-handler: parse json: ") {
		t.Errorf("stderr prefix: want %q, got %q", "hook-handler: parse json: ", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("stderr must end with newline, got %q", got)
	}
}
