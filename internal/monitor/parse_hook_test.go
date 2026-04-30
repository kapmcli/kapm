package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/testutil"
)

func writeHookJSONL(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hook.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func hookLine(ts time.Time) string {
	b, err := json.Marshal(HookRecord{Ts: ts, Session: "sess-1", Event: "preToolUse"})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestParseHookRecords_GarbageLineWarn asserts that a JSON-parse failure
// emits slog.Warn with path and count=1.
func TestParseHookRecords_GarbageLineWarn(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	path := writeHookJSONL(t, []string{
		hookLine(ts),
		`not json`,
	})

	buf, restore := testutil.CaptureSlog(t)
	defer restore()

	recs, err := parseHookRecords(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}

	log := buf.String()
	if !strings.Contains(log, "level=WARN") {
		t.Errorf("expected WARN in log, got: %s", log)
	}
	if !strings.Contains(log, path) {
		t.Errorf("expected path %q in log, got: %s", path, log)
	}
	if !strings.Contains(log, "count=1") {
		t.Errorf("expected count=1 in log, got: %s", log)
	}
}

// TestParseHookRecords_OldFormatSilent asserts that structurally-valid JSON
// with no ts field is silently skipped (no Warn emitted).
func TestParseHookRecords_OldFormatSilent(t *testing.T) {
	path := writeHookJSONL(t, []string{
		`{"legacy":"thing"}`,
	})

	buf, restore := testutil.CaptureSlog(t)
	defer restore()

	recs, err := parseHookRecords(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("got %d records, want 0", len(recs))
	}

	log := buf.String()
	if strings.Contains(log, "level=WARN") {
		t.Errorf("expected no WARN for old-format line, got: %s", log)
	}
}

// TestParseHookRecords_MixedValidAndGarbage asserts partial success with Warn.
func TestParseHookRecords_MixedValidAndGarbage(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	path := writeHookJSONL(t, []string{
		hookLine(ts),
		hookLine(ts.Add(time.Second)),
		`{bad json`,
	})

	buf, restore := testutil.CaptureSlog(t)
	defer restore()

	recs, err := parseHookRecords(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}

	log := buf.String()
	if !strings.Contains(log, "level=WARN") {
		t.Errorf("expected WARN in log, got: %s", log)
	}
	if !strings.Contains(log, "count=1") {
		t.Errorf("expected count=1 in log, got: %s", log)
	}
}
