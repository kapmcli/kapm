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

func buildIDEFixture(t *testing.T, dir, wsPath string, sessions []IDESessionEntry, histories map[string]IDESessionHistory) {
	t.Helper()
	enc := base64.RawURLEncoding.EncodeToString([]byte(wsPath))
	wsDir := filepath.Join(dir, "workspace-sessions", enc)
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(sessions)
	if err := os.WriteFile(filepath.Join(wsDir, "sessions.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	for id, h := range histories {
		b, _ := json.Marshal(h)
		if err := os.WriteFile(filepath.Join(wsDir, id+".json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func makeHistory(execIDs ...string) IDESessionHistory {
	var entries []IDEHistoryEntry
	for _, id := range execIDs {
		entries = append(entries, IDEHistoryEntry{ExecutionID: id})
	}
	return IDESessionHistory{History: entries}
}

func TestLoadIDESessions_Valid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sessions := []IDESessionEntry{
		{SessionID: "sess-001", Title: "Feature X", DateCreated: "1777435222255"},
		{SessionID: "sess-002", Title: "Fix bug", DateCreated: "1777521622255"},
	}
	buildIDEFixture(t, dir, "/home/user/project-alpha", sessions, map[string]IDESessionHistory{
		"sess-001": makeHistory("exec-001", "exec-002"),
		"sess-002": makeHistory("exec-003"),
	})

	got, err := LoadIDESessions(context.Background(), dir, time.Time{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(got))
	}
	if got[0].SessionID != "sess-001" {
		t.Errorf("want sess-001, got %s", got[0].SessionID)
	}
	if len(got[0].ExecutionIDs) != 2 {
		t.Errorf("want 2 execIDs, got %v", got[0].ExecutionIDs)
	}
	if got[0].WorkspaceDirectory != "/home/user/project-alpha" {
		t.Errorf("wrong workspace: %s", got[0].WorkspaceDirectory)
	}
}

func TestLoadIDESessions_CwdFilter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	buildIDEFixture(t, dir, "/home/user/project-alpha",
		[]IDESessionEntry{{SessionID: "sess-a", DateCreated: "1777435222255"}},
		map[string]IDESessionHistory{"sess-a": makeHistory("exec-a")})
	buildIDEFixture(t, dir, "/home/user/project-beta",
		[]IDESessionEntry{{SessionID: "sess-b", DateCreated: "1777435222255"}},
		map[string]IDESessionHistory{"sess-b": makeHistory("exec-b")})

	got, err := LoadIDESessions(context.Background(), dir, time.Time{}, "/home/user/project-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SessionID != "sess-a" {
		t.Errorf("want [sess-a], got %v", got)
	}
}

func TestLoadIDESessions_SinceFilter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	buildIDEFixture(t, dir, "/home/user/project-alpha",
		[]IDESessionEntry{
			{SessionID: "sess-old", DateCreated: "1777435222255"},
			{SessionID: "sess-new", DateCreated: "1777521622255"},
		},
		map[string]IDESessionHistory{
			"sess-old": makeHistory("exec-001"),
			"sess-new": makeHistory("exec-002"),
		})

	since := time.UnixMilli(1777521622255)
	got, err := LoadIDESessions(context.Background(), dir, since, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SessionID != "sess-new" {
		t.Errorf("want [sess-new], got %v", got)
	}
}

func TestLoadIDESessions_EmptyDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "workspace-sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := LoadIDESessions(context.Background(), dir, time.Time{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("want empty, got %v", got)
	}
}

func TestLoadIDESessions_MissingDir(t *testing.T) {
	t.Parallel()
	got, err := LoadIDESessions(context.Background(), "/nonexistent/path/does/not/exist", time.Time{}, "")
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
	if got != nil {
		t.Errorf("want nil, got %v", got)
	}
}

func TestDecodeWorkspacePath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input    string
		expected string
	}{
		{"L1VzZXJzL2ltc2svd29ya3NwYWNlcy9rYXBt", "/Users/imsk/workspaces/kapm"},
		{"L2hvbWUvZWMyLXVzZXIvd29yay9kZW1vLTAxMA__", "/home/ec2-user/work/demo-010"},
		{base64.RawURLEncoding.EncodeToString([]byte("/home/user/project-alpha")), "/home/user/project-alpha"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got, err := decodeWorkspacePath(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.expected {
				t.Errorf("want %q, got %q", tc.expected, got)
			}
		})
	}
}
