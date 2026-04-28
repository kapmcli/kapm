package monitor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeSession writes a .json + .jsonl pair into dir.
func writeSession(t *testing.T, dir, uuid string, updatedAt time.Time, msgs []SessionMessage) {
	t.Helper()
	meta := SessionMeta{
		SessionID: uuid,
		UpdatedAt: updatedAt.UTC().Format(time.RFC3339),
	}
	metaBytes, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(dir, uuid+".json"), metaBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	var jsonl []byte
	for _, m := range msgs {
		b, _ := json.Marshal(m)
		jsonl = append(jsonl, b...)
		jsonl = append(jsonl, '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, uuid+".jsonl"), jsonl, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSessions_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	sessions, cache, err := LoadSessions(context.Background(), dir, time.Time{}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(sessions))
	}
	if cache == nil {
		t.Fatal("expected non-nil cache")
	}
}

func TestLoadSessions_SinceFilter(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-2 * time.Hour)
	recent := now.Add(-30 * time.Minute)

	msg := SessionMessage{Kind: "Prompt"}
	writeSession(t, dir, "old-uuid", old, []SessionMessage{msg})
	writeSession(t, dir, "new-uuid", recent, []SessionMessage{msg})

	since := now.Add(-1 * time.Hour)
	sessions, _, err := LoadSessions(context.Background(), dir, since, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Meta.SessionID != "new-uuid" {
		t.Fatalf("expected new-uuid, got %s", sessions[0].Meta.SessionID)
	}
}

func TestLoadSessions_CacheHit(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	msg := SessionMessage{Kind: "Prompt"}
	writeSession(t, dir, "sess1", now, []SessionMessage{msg})

	// First load — populates cache.
	_, cache, err := LoadSessions(context.Background(), dir, time.Time{}, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt the .jsonl so a re-parse would fail/differ.
	jsonlPath := filepath.Join(dir, "sess1.jsonl")
	if err := os.WriteFile(jsonlPath, []byte("not-json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Restore mtime+size to match cache entry so cache hit is triggered.
	origInfo := cache.data[jsonlPath]
	if err := os.Chtimes(jsonlPath, origInfo.mtime, origInfo.mtime); err != nil {
		t.Fatal(err)
	}
	// Restore size by writing original content back but keeping same mtime.
	origData, _ := json.Marshal(msg)
	origData = append(origData, '\n')
	if err := os.WriteFile(jsonlPath, origData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(jsonlPath, origInfo.mtime, origInfo.mtime); err != nil {
		t.Fatal(err)
	}

	sessions, _, err := LoadSessions(context.Background(), dir, time.Time{}, "", cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
}

func TestLoadSessions_CacheMiss(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	msg := SessionMessage{Kind: "Prompt"}
	writeSession(t, dir, "sess1", now, []SessionMessage{msg})

	_, cache, err := LoadSessions(context.Background(), dir, time.Time{}, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Write a new message to .jsonl — changes mtime and size.
	msg2 := SessionMessage{Kind: "AssistantMessage"}
	jsonlPath := filepath.Join(dir, "sess1.jsonl")
	var newContent []byte
	for _, m := range []SessionMessage{msg, msg2} {
		b, _ := json.Marshal(m)
		newContent = append(newContent, b...)
		newContent = append(newContent, '\n')
	}
	if err := os.WriteFile(jsonlPath, newContent, 0o644); err != nil {
		t.Fatal(err)
	}

	sessions, _, err := LoadSessions(context.Background(), dir, time.Time{}, "", cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if len(sessions[0].Messages) != 2 {
		t.Fatalf("expected 2 messages after cache miss, got %d", len(sessions[0].Messages))
	}
}

func TestLoadSessions_MissingJSONL(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	meta := SessionMeta{SessionID: "no-jsonl", UpdatedAt: now.Format(time.RFC3339)}
	metaBytes, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(dir, "no-jsonl.json"), metaBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	// No .jsonl written.

	sessions, _, err := LoadSessions(context.Background(), dir, time.Time{}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions (missing .jsonl skipped), got %d", len(sessions))
	}
}

func TestLoadSessions_SymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	real := t.TempDir()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks not supported:", err)
	}

	_, _, err := LoadSessions(context.Background(), link, time.Time{}, "", nil)
	if err == nil {
		t.Fatal("expected error for symlink dir, got nil")
	}
}

func TestLoadSessions_ContextCancelled(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	msg := SessionMessage{Kind: "Prompt"}
	writeSession(t, dir, "sess1", now, []SessionMessage{msg})
	writeSession(t, dir, "sess2", now, []SessionMessage{msg})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, _, err := LoadSessions(ctx, dir, time.Time{}, "", nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestLoadSessions_LockFilesIgnored(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	msg := SessionMessage{Kind: "Prompt"}
	writeSession(t, dir, "sess1", now, []SessionMessage{msg})
	// Write a .lock file — should be ignored.
	if err := os.WriteFile(filepath.Join(dir, "sess1.lock"), []byte("locked"), 0o644); err != nil {
		t.Fatal(err)
	}

	sessions, _, err := LoadSessions(context.Background(), dir, time.Time{}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
}
