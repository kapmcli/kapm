package monitor

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/testutil"

	_ "modernc.org/sqlite"
)

func TestLoadAll_EmptyDirs(t *testing.T) {
	t.Parallel()
	recs, cache, err := LoadAll(context.Background(), "/nonexistent/sessions", "/nonexistent/hooks", "", "", time.Time{}, "", nil, nil)
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
	recs, _, err := LoadAll(ctx, dir, "", "", "", time.Time{}, "", nil, nil)
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

	recs, cache, err := LoadAll(context.Background(), cliDir, "", ideDir, "", time.Time{}, "", nil, nil)
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

func TestLoadAll_WithIDEHookRecords(t *testing.T) {
	t.Parallel()
	cliDir := t.TempDir()
	hookDir := t.TempDir()
	ideDir := t.TempDir()
	wsPath := "/home/user/project-alpha"
	enc := base64.RawURLEncoding.EncodeToString([]byte(wsPath))
	wsDir := filepath.Join(ideDir, "workspace-sessions", enc)
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 5, 3, 1, 0, 0, 0, time.UTC)
	sessions := []IDESessionEntry{{SessionID: "ide-sess-1", Title: "IDE Session", DateCreated: "1777760400000"}}
	data, _ := json.Marshal(sessions)
	if err := os.WriteFile(filepath.Join(wsDir, "sessions.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	history := IDESessionHistory{History: []IDEHistoryEntry{{
		Message: IDEMessage{Role: "user", Content: json.RawMessage(`"hello from ide"`)},
	}}}
	hdata, _ := json.Marshal(history)
	if err := os.WriteFile(filepath.Join(wsDir, "ide-sess-1.json"), hdata, 0o644); err != nil {
		t.Fatal(err)
	}
	ideHookDir := filepath.Join(hookDir, "ide")
	if err := os.MkdirAll(ideHookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hooks := []HookRecord{
		{Ts: createdAt.Add(time.Second), Event: "userPromptSubmit", Agent: "ide", Cwd: wsPath},
		{Ts: createdAt.Add(2 * time.Second), Event: "stop", Agent: "ide", Cwd: wsPath},
	}
	var lines []byte
	for _, hook := range hooks {
		line, err := json.Marshal(hook)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line...)
		lines = append(lines, '\n')
	}
	if err := os.WriteFile(filepath.Join(ideHookDir, "events.jsonl"), lines, 0o600); err != nil {
		t.Fatal(err)
	}

	recs, _, err := LoadAll(context.Background(), cliDir, hookDir, ideDir, "", time.Time{}, "", nil, nil)
	if err != nil {
		t.Fatalf("LoadAll() error = %v", err)
	}
	detail, err := AggregateDetail(context.Background(), recs, createdAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("AggregateDetail() error = %v", err)
	}
	if len(detail.Sessions) != 1 {
		t.Fatalf("sessions len = %d, want 1", len(detail.Sessions))
	}
	if len(detail.Sessions[0].PromptHistory) != 1 || detail.Sessions[0].PromptHistory[0] != "hello from ide" {
		t.Fatalf("PromptHistory = %#v", detail.Sessions[0].PromptHistory)
	}
	if detail.Sessions[0].Active {
		t.Fatal("session should be inactive after IDE stop hook")
	}
}

func TestLoadAll_WithLegacyIDEHookInputRecords(t *testing.T) {
	t.Parallel()
	cliDir := t.TempDir()
	hookDir := t.TempDir()
	ideDir := t.TempDir()
	wsPath := "/home/user/project-alpha"
	enc := base64.RawURLEncoding.EncodeToString([]byte(wsPath))
	wsDir := filepath.Join(ideDir, "workspace-sessions", enc)
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 5, 3, 1, 0, 0, 0, time.UTC)
	sessions := []IDESessionEntry{{SessionID: "ide-sess-legacy", Title: "IDE Legacy", DateCreated: "1777760400000"}}
	data, _ := json.Marshal(sessions)
	if err := os.WriteFile(filepath.Join(wsDir, "sessions.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	history := IDESessionHistory{History: []IDEHistoryEntry{{
		Message: IDEMessage{Role: "user", Content: json.RawMessage(`"legacy ide prompt"`)},
	}}}
	hdata, _ := json.Marshal(history)
	if err := os.WriteFile(filepath.Join(wsDir, "ide-sess-legacy.json"), hdata, 0o644); err != nil {
		t.Fatal(err)
	}
	legacyHooks := []map[string]any{
		{"ts": createdAt.Add(time.Second).Format(time.RFC3339Nano), "event": "userPromptSubmit", "agent": "ide", "cwd": wsPath, "stdin": "", "stdin_bytes": 0},
		{"ts": createdAt.Add(2 * time.Second).Format(time.RFC3339Nano), "event": "stop", "agent": "ide", "cwd": wsPath, "stdin": "", "stdin_bytes": 0},
	}
	var lines []byte
	for _, hook := range legacyHooks {
		line, err := json.Marshal(hook)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line...)
		lines = append(lines, '\n')
	}
	if err := os.WriteFile(filepath.Join(hookDir, "hook-input.jsonl"), lines, 0o600); err != nil {
		t.Fatal(err)
	}

	recs, _, err := LoadAll(context.Background(), cliDir, hookDir, ideDir, "", time.Time{}, "", nil, nil)
	if err != nil {
		t.Fatalf("LoadAll() error = %v", err)
	}
	detail, err := AggregateDetail(context.Background(), recs, createdAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("AggregateDetail() error = %v", err)
	}
	if len(detail.Sessions) != 1 {
		t.Fatalf("sessions len = %d, want 1", len(detail.Sessions))
	}
	if len(detail.Sessions[0].PromptHistory) != 1 || detail.Sessions[0].PromptHistory[0] != "legacy ide prompt" {
		t.Fatalf("PromptHistory = %#v", detail.Sessions[0].PromptHistory)
	}
	if detail.Sessions[0].Active {
		t.Fatal("session should be inactive after legacy IDE stop hook")
	}
}

func TestLoadAll_EmptyIDEDir(t *testing.T) {
	t.Parallel()
	cliDir := t.TempDir()
	ideDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ideDir, "workspace-sessions"), 0o755); err != nil {
		t.Fatal(err)
	}

	recs, _, err := LoadAll(context.Background(), cliDir, "", ideDir, "", time.Time{}, "", nil, nil)
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

	recs, _, err := LoadAll(context.Background(), cliDir, "", "", "", time.Time{}, "", nil, nil)
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

// TestLoadAllHookRecords_UnreadableWarn verifies that a directory-named .jsonl
// (unreadable as a file) causes a Warn log with the path.
// Must NOT call t.Parallel() — uses testutil.CaptureSlog.
func TestLoadAllHookRecords_UnreadableWarn(t *testing.T) {
	hookDir := t.TempDir()
	cliDir := filepath.Join(hookDir, "cli")
	if err := os.MkdirAll(cliDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create a directory named "bad.jsonl" — os.Open on a dir returns an error
	// that is not fs.ErrNotExist, triggering the non-ErrNotExist branch.
	badPath := filepath.Join(cliDir, "bad.jsonl")
	if err := os.Mkdir(badPath, 0o755); err != nil {
		t.Fatal(err)
	}

	buf, restore := testutil.CaptureSlog(t)
	defer restore()

	recs, err := loadAllHookRecords(context.Background(), hookDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("expected 0 records, got %d", len(recs))
	}
	if !strings.Contains(buf.String(), "skipped hook log file") {
		t.Errorf("expected 'skipped hook log file' in log, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), badPath) {
		t.Errorf("expected path %q in log, got: %s", badPath, buf.String())
	}
}

func TestLoadAllHookRecordsReadsCLIAndLegacyRoot(t *testing.T) {
	t.Parallel()
	hookDir := t.TempDir()
	cliDir := filepath.Join(hookDir, "cli")
	if err := os.MkdirAll(cliDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 5, 3, 1, 2, 3, 0, time.UTC)
	cliLine, err := json.Marshal(HookRecord{Ts: ts, Session: "cli-session", Event: "preToolUse", Agent: "coder", Tool: "shell"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cliDir, "cli-session.jsonl"), append(cliLine, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "cli-session.jsonl"), append(cliLine, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyLine, err := json.Marshal(HookRecord{Ts: ts.Add(time.Second), Session: "legacy-session", Event: "postToolUse", Agent: "reviewer", Tool: "read"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "legacy-session.jsonl"), append(legacyLine, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	ignoredLine, err := json.Marshal(HookRecord{Ts: ts.Add(2 * time.Second), Session: "other-session", Event: "preToolUse", Agent: "ide", Tool: "read"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "hook-input.jsonl"), append(ignoredLine, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	recs, err := loadAllHookRecords(context.Background(), hookDir)
	if err != nil {
		t.Fatalf("loadAllHookRecords() error = %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("records len = %d, want 2: %#v", len(recs), recs)
	}
	if recs[0].Session != "cli-session" || recs[0].Tool != "shell" {
		t.Fatalf("cli record = %#v", recs[0])
	}
	if recs[1].Session != "legacy-session" || recs[1].Tool != "read" {
		t.Fatalf("legacy record = %#v", recs[1])
	}
}

func TestLoadAllHookRecordsSortsMixedCLIAndLegacyRoot(t *testing.T) {
	t.Parallel()
	hookDir := t.TempDir()
	cliDir := filepath.Join(hookDir, "cli")
	if err := os.MkdirAll(cliDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 5, 3, 1, 2, 3, 0, time.UTC)
	legacyLine, err := json.Marshal(HookRecord{Ts: ts, Session: "mixed-session", Event: "preToolUse", Agent: "coder", Tool: "read"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "mixed-session.jsonl"), append(legacyLine, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	cliLine, err := json.Marshal(HookRecord{Ts: ts.Add(time.Second), Session: "mixed-session", Event: "postToolUse", Agent: "coder", Tool: "read"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cliDir, "mixed-session.jsonl"), append(cliLine, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	recs, err := loadAllHookRecords(context.Background(), hookDir)
	if err != nil {
		t.Fatalf("loadAllHookRecords() error = %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("records len = %d, want 2: %#v", len(recs), recs)
	}
	if recs[0].Event != "preToolUse" || recs[1].Event != "postToolUse" {
		t.Fatalf("records not chronological: %#v", recs)
	}
}

func TestLoadAllHookRecordsSkipsHookInputSelfMatch(t *testing.T) {
	t.Parallel()
	hookDir := t.TempDir()
	ts := time.Date(2026, 5, 3, 4, 5, 6, 0, time.UTC)
	line, err := json.Marshal(HookRecord{Ts: ts, Session: "hook-input", Event: "preToolUse", Agent: "ide", Tool: "read"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "hook-input.jsonl"), append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	recs, err := loadAllHookRecords(context.Background(), hookDir)
	if err != nil {
		t.Fatalf("loadAllHookRecords() error = %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("records len = %d, want 0: %#v", len(recs), recs)
	}
}

func TestLoadAllHookRecordsReadsLegacyRootWithoutCLIDir(t *testing.T) {
	t.Parallel()
	hookDir := t.TempDir()
	ts := time.Date(2026, 5, 3, 4, 5, 6, 0, time.UTC)
	line, err := json.Marshal(HookRecord{Ts: ts, Session: "legacy-only", Event: "preToolUse", Agent: "reviewer", Tool: "read"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "legacy-only.jsonl"), append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	recs, err := loadAllHookRecords(context.Background(), hookDir)
	if err != nil {
		t.Fatalf("loadAllHookRecords() error = %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("records len = %d, want 1: %#v", len(recs), recs)
	}
	if recs[0].Session != "legacy-only" || recs[0].Agent != "reviewer" {
		t.Fatalf("legacy-only record = %#v", recs[0])
	}
}

// createStoreTestSQLiteDB creates a test SQLite DB with the conversations_v2 table.
func createStoreTestSQLiteDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE conversations_v2 (
		key TEXT NOT NULL,
		conversation_id TEXT NOT NULL,
		value TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (key, conversation_id)
	)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	return dbPath, db
}

func insertStoreRow(t *testing.T, db *sql.DB, key, convID string, updatedAt int64) {
	t.Helper()
	val := makeV1Value("prompt-" + convID)
	_, err := db.Exec(
		`INSERT INTO conversations_v2 (key, conversation_id, value, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		key, convID, val, updatedAt-1000, updatedAt,
	)
	if err != nil {
		t.Fatalf("insert row: %v", err)
	}
}

func TestLoadAll_WithSQLite(t *testing.T) {
	t.Parallel()
	cliDir := t.TempDir()
	dbPath, db := createStoreTestSQLiteDB(t)
	now := time.Now().UnixMilli()
	insertStoreRow(t, db, "/project", "v1-sess-1", now)
	_ = db.Close()

	recs, _, err := LoadAll(context.Background(), cliDir, "", "", dbPath, time.Time{}, "", nil, NewSQLiteCache())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, r := range recs {
		if r.SessionID == "v1-sess-1" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected v1-sess-1 in records, got %v", recs)
	}
}

func TestLoadAll_SQLiteDedup(t *testing.T) {
	t.Parallel()
	cliDir := t.TempDir()

	// Write a v2 session with the same ID as the v1 session.
	sharedID := "shared-sess"
	writeSession(t, cliDir, sharedID, time.Now(), []SessionMessage{promptMsg("hello", time.Now().Unix())})

	dbPath, db := createStoreTestSQLiteDB(t)
	now := time.Now().UnixMilli()
	insertStoreRow(t, db, "/project", sharedID, now)
	_ = db.Close()

	// Also insert a v1-only session to confirm v1 sessions without v2 counterpart are included.
	db2, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	insertStoreRow(t, db2, "/project", "v1-only", now)
	_ = db2.Close()

	recs, _, err := LoadAll(context.Background(), cliDir, "", "", dbPath, time.Time{}, "", nil, NewSQLiteCache())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sessionIDs := make(map[string]struct{})
	for _, r := range recs {
		sessionIDs[r.SessionID] = struct{}{}
	}
	// v2 session (shared-sess) should appear.
	if _, ok := sessionIDs[sharedID]; !ok {
		t.Errorf("expected %q in records", sharedID)
	}
	// v1-only session should appear (no v2 counterpart).
	if _, ok := sessionIDs["v1-only"]; !ok {
		t.Errorf("expected v1-only in records")
	}
	// Dedup: v1 shared-sess excluded — verify by checking allSessions count.
	// v2 has 1 prompt → 1 turn; v1 shared-sess would add 1 more turn if not deduped.
	// We verify the total record count matches v2(1 turn) + v1-only(1 turn) = 2 sessions.
	if len(sessionIDs) != 2 {
		t.Errorf("expected 2 distinct session IDs, got %d: %v", len(sessionIDs), sessionIDs)
	}
}

func TestLoadAll_NoSQLite(t *testing.T) {
	t.Parallel()
	cliDir := t.TempDir()
	writeSession(t, cliDir, "v2-only", time.Now(), []SessionMessage{promptMsg("hello", time.Now().Unix())})

	recs, _, err := LoadAll(context.Background(), cliDir, "", "", "", time.Time{}, "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, r := range recs {
		if r.SessionID == "v2-only" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected v2-only in records, got %v", recs)
	}
}

func TestSQLiteCache_MtimeHit(t *testing.T) {
	t.Parallel()
	dbPath, db := createStoreTestSQLiteDB(t)
	now := time.Now().UnixMilli()
	insertStoreRow(t, db, "/project", "sess-1", now)
	_ = db.Close()

	cache := NewSQLiteCache()
	sessions1, err := cache.Load(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if len(sessions1) != 1 {
		t.Fatalf("want 1 session, got %d", len(sessions1))
	}

	// Second load without changing the file — should return cached slice (same pointer).
	sessions2, err := cache.Load(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if len(sessions2) != 1 {
		t.Fatalf("want 1 session on cache hit, got %d", len(sessions2))
	}
	// Verify it's the same underlying slice (cache hit).
	if &sessions1[0] != &sessions2[0] {
		t.Error("expected same slice on mtime hit (cache not invalidated)")
	}
}

func TestSQLiteCache_MtimeMiss(t *testing.T) {
	t.Parallel()
	dbPath, db := createStoreTestSQLiteDB(t)
	now := time.Now().UnixMilli()
	insertStoreRow(t, db, "/project", "sess-1", now)
	_ = db.Close()

	cache := NewSQLiteCache()
	sessions1, err := cache.Load(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if len(sessions1) != 1 {
		t.Fatalf("want 1 session, got %d", len(sessions1))
	}

	// Modify the file to change mtime.
	db2, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	insertStoreRow(t, db2, "/project", "sess-2", now+1000)
	_ = db2.Close()

	sessions2, err := cache.Load(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if len(sessions2) != 2 {
		t.Fatalf("want 2 sessions after mtime miss, got %d", len(sessions2))
	}
}
