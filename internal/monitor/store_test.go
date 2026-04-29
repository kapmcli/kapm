package monitor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadAll_EmptyDirs(t *testing.T) {
	t.Parallel()
	recs, cache, err := LoadAll(context.Background(), "/nonexistent/sessions", "/nonexistent/hooks", "", time.Time{}, "", nil)
	if err != nil {
		t.Fatalf("expected nil error for missing dirs, got: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("expected empty slice, got %d records", len(recs))
	}
	if cache == nil {
		t.Error("expected non-nil cache")
	}
}

func TestLoadAll_CancelledCtx(t *testing.T) {
	t.Parallel()
	// With an empty dir and cancelled ctx, LoadSessions returns empty without error
	// because the loop body never executes. This is acceptable behavior.
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	recs, _, err := LoadAll(ctx, dir, "", "", time.Time{}, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("expected empty records, got %d", len(recs))
	}
}

func TestLoadAll_WithIDESessions(t *testing.T) {
	t.Parallel()
	// Set up CLI sessions dir (empty — no CLI sessions)
	cliDir := t.TempDir()

	// Set up IDE fixture
	ideDir := t.TempDir()
	wsPath := "/home/user/project-alpha"
	enc := base64.RawURLEncoding.EncodeToString([]byte(wsPath))
	wsDir := filepath.Join(ideDir, "workspace-sessions", enc)
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessions := []IDESessionEntry{{SessionID: "ide-sess-1", Title: "IDE Session", DateCreated: "1777435222255"}}
	data, _ := json.Marshal(sessions)
	if err := os.WriteFile(filepath.Join(wsDir, "sessions.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	history := IDESessionHistory{History: []IDEHistoryEntry{{ExecutionID: "exec-1"}}}
	hdata, _ := json.Marshal(history)
	if err := os.WriteFile(filepath.Join(wsDir, "ide-sess-1.json"), hdata, 0o644); err != nil {
		t.Fatal(err)
	}

	recs, cache, err := LoadAll(context.Background(), cliDir, "", ideDir, time.Time{}, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cache == nil {
		t.Error("expected non-nil cache")
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 IDE record, got %d", len(recs))
	}
	if recs[0].SessionID != "ide-sess-1" {
		t.Errorf("expected SessionID=ide-sess-1, got %q", recs[0].SessionID)
	}
	if recs[0].Agent != "kiro-ide" {
		t.Errorf("expected Agent=kiro-ide, got %q", recs[0].Agent)
	}
}

func TestLoadAll_EmptyIDEDir(t *testing.T) {
	t.Parallel()
	cliDir := t.TempDir()
	ideDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ideDir, "workspace-sessions"), 0o755); err != nil {
		t.Fatal(err)
	}

	recs, _, err := LoadAll(context.Background(), cliDir, "", ideDir, time.Time{}, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("expected empty records, got %d", len(recs))
	}
}

func TestLoadAll_NoIDEDir(t *testing.T) {
	t.Parallel()
	cliDir := t.TempDir()

	recs, _, err := LoadAll(context.Background(), cliDir, "", "", time.Time{}, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("expected empty records, got %d", len(recs))
	}
}

func TestCollectExecutionIDs(t *testing.T) {
	t.Parallel()
	sessions := []IDEParsedSession{
		{ExecutionIDs: []string{"a", "b"}},
		{ExecutionIDs: []string{"b", "c"}},
	}
	got := collectExecutionIDs(sessions)
	if len(got) != 3 {
		t.Errorf("expected 3 unique IDs, got %d", len(got))
	}
	for _, id := range []string{"a", "b", "c"} {
		if _, ok := got[id]; !ok {
			t.Errorf("missing ID %q", id)
		}
	}
}
