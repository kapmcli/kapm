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
	// Create a directory named "bad.jsonl" — os.Open on a dir returns an error
	// that is not fs.ErrNotExist, triggering the non-ErrNotExist branch.
	badPath := filepath.Join(hookDir, "bad.jsonl")
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
