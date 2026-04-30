package monitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func createTestSQLiteDB(t *testing.T) (string, *sql.DB) {
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

func makeV1Value(prompt string) string {
	val := v1Value{
		History: []v1HistoryEntry{
			{
				User: v1UserTurn{Content: v1UserContent{Prompt: &v1Prompt{Prompt: prompt}}},
				Assistant: v1AssistantTurn{Response: &v1Response{MessageID: "msg1", Content: "reply"}},
			},
		},
	}
	b, _ := json.Marshal(val)
	return string(b)
}

func insertRow(t *testing.T, db *sql.DB, key, convID, value string, createdAt, updatedAt int64) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO conversations_v2 (key, conversation_id, value, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		key, convID, value, createdAt, updatedAt,
	)
	if err != nil {
		t.Fatalf("insert row: %v", err)
	}
}

func TestLoadSessionsSQLite_Basic(t *testing.T) {
	dbPath, db := createTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	now := time.Now().UnixMilli()
	insertRow(t, db, "/project", "conv-1", makeV1Value("hello"), now-1000, now)
	_ = db.Close()

	sessions, err := LoadSessionsSQLite(context.Background(), dbPath, time.Time{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.Meta.SessionID != "conv-1" {
		t.Errorf("SessionID = %q, want %q", s.Meta.SessionID, "conv-1")
	}
	if s.Meta.Cwd != "/project" {
		t.Errorf("Cwd = %q, want %q", s.Meta.Cwd, "/project")
	}
	if s.Meta.Title != "hello" {
		t.Errorf("Title = %q, want %q", s.Meta.Title, "hello")
	}
}

func TestLoadSessionsSQLite_SinceFilter(t *testing.T) {
	dbPath, db := createTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	base := time.Now().UnixMilli()
	insertRow(t, db, "/project", "old", makeV1Value("old"), base-10000, base-5000)
	insertRow(t, db, "/project", "new", makeV1Value("new"), base-1000, base)
	_ = db.Close()

	since := time.UnixMilli(base - 2000)
	sessions, err := LoadSessionsSQLite(context.Background(), dbPath, since, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(sessions))
	}
	if sessions[0].Meta.SessionID != "new" {
		t.Errorf("SessionID = %q, want %q", sessions[0].Meta.SessionID, "new")
	}
}

func TestLoadSessionsSQLite_CwdFilter(t *testing.T) {
	dbPath, db := createTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	now := time.Now().UnixMilli()
	insertRow(t, db, "/project-alpha", "a", makeV1Value("a"), now-2000, now-1000)
	insertRow(t, db, "/project-alpha/sub", "b", makeV1Value("b"), now-2000, now-500)
	insertRow(t, db, "/project-beta", "c", makeV1Value("c"), now-2000, now)
	_ = db.Close()

	sessions, err := LoadSessionsSQLite(context.Background(), dbPath, time.Time{}, "/project-alpha")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(sessions))
	}
	for _, s := range sessions {
		if s.Meta.Cwd != "/project-alpha" && s.Meta.Cwd != "/project-alpha/sub" {
			t.Errorf("unexpected Cwd %q", s.Meta.Cwd)
		}
	}
}

func TestLoadSessionsSQLite_NoDB(t *testing.T) {
	sessions, err := LoadSessionsSQLite(context.Background(), "/nonexistent/path/db.sqlite", time.Time{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("want 0 sessions, got %d", len(sessions))
	}

	sessions2, err2 := LoadSessionsSQLite(context.Background(), "", time.Time{}, "")
	if err2 != nil {
		t.Fatalf("unexpected error for empty path: %v", err2)
	}
	if len(sessions2) != 0 {
		t.Fatalf("want 0 sessions for empty path, got %d", len(sessions2))
	}
}

func TestLoadSessionsSQLite_EmptyDB(t *testing.T) {
	dbPath, db := createTestSQLiteDB(t)
	_ = db.Close()

	sessions, err := LoadSessionsSQLite(context.Background(), dbPath, time.Time{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("want 0 sessions, got %d", len(sessions))
	}

	// Verify it's a non-nil empty slice.
	if sessions == nil {
		t.Error("want non-nil empty slice, got nil")
	}
}

func TestLoadSessionsSQLite_TempDir(t *testing.T) {
	// dbPath pointing to a directory (not a file) should return empty, no error.
	dir := t.TempDir()
	sessions, err := LoadSessionsSQLite(context.Background(), filepath.Join(dir, "missing.db"), time.Time{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("want 0 sessions, got %d", len(sessions))
	}
}

func TestLoadSessionsSQLite_SkipBadRow(t *testing.T) {
	dbPath, db := createTestSQLiteDB(t)
	defer func() { _ = db.Close() }()

	now := time.Now().UnixMilli()
	// Insert a row with invalid JSON value — should be skipped.
	insertRow(t, db, "/project", "bad", "not-valid-json", now-1000, now)
	insertRow(t, db, "/project", "good", makeV1Value("good"), now-1000, now)
	_ = db.Close()

	sessions, err := LoadSessionsSQLite(context.Background(), dbPath, time.Time{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("want 1 session (bad row skipped), got %d", len(sessions))
	}
	if sessions[0].Meta.SessionID != "good" {
		t.Errorf("SessionID = %q, want %q", sessions[0].Meta.SessionID, "good")
	}
}

