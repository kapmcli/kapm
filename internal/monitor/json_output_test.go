package monitor_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/monitor"
)

// setupTestSessions creates a minimal sessions dir with one session for testing.
func setupTestSessions(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	meta := `{"session_id":"sess-1","title":"test","cwd":"/w","created_at":"2026-04-20T06:00:00Z","updated_at":"2026-04-20T08:00:00Z","session_state":{"agent_name":"kiro"}}`
	jsonl := `{"version":"1","kind":"Prompt","data":{"message_id":"m1","content":[{"kind":"text","data":"\"hello\""}],"meta":{"timestamp":1745128800}}}
{"version":"1","kind":"AssistantMessage","data":{"message_id":"m2","content":[{"kind":"toolUse","data":{"toolUseId":"tu-1","name":"bash","input":{"command":"echo hi"}}}]}}
{"version":"1","kind":"ToolResults","data":{"content":[{"kind":"toolResult","data":{"toolUseId":"tu-1","content":[],"status":"success"}}]}}
`
	if err := os.WriteFile(filepath.Join(dir, "sess-1.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sess-1.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRunJSON_NoFilter(t *testing.T) {
	t.Parallel()
	sessDir := setupTestSessions(t)
	var buf bytes.Buffer
	if err := monitor.RunJSON(context.Background(), sessDir, "", "", "", "", 8760*time.Hour, "", "", &buf); err != nil {
		t.Fatalf("RunJSON: %v", err)
	}
	var dm monitor.DetailedMetrics
	if err := json.Unmarshal(buf.Bytes(), &dm); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(dm.Sessions) == 0 {
		t.Error("expected sessions, got none")
	}
}

func TestRunJSON_SessionFilter(t *testing.T) {
	t.Parallel()
	sessDir := setupTestSessions(t)
	var buf bytes.Buffer
	if err := monitor.RunJSON(context.Background(), sessDir, "", "", "", "", 8760*time.Hour, "sess-1", "", &buf); err != nil {
		t.Fatalf("RunJSON: %v", err)
	}
	var dm monitor.DetailedMetrics
	if err := json.Unmarshal(buf.Bytes(), &dm); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(dm.Sessions) != 1 {
		t.Fatalf("expected 1 merged session, got %d", len(dm.Sessions))
	}
	s := dm.Sessions[0]
	if s.ID != "sess-1" {
		t.Errorf("expected ID=sess-1, got %q", s.ID)
	}
}

func TestRunJSON_NoMatchSession(t *testing.T) {
	t.Parallel()
	sessDir := setupTestSessions(t)
	var buf bytes.Buffer
	err := monitor.RunJSON(context.Background(), sessDir, "", "", "", "", 8760*time.Hour, "nonexistent-sid", "", &buf)
	if err == nil {
		t.Fatal("expected error for missing session, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent-sid") {
		t.Errorf("error %q does not contain sid", err.Error())
	}
}

func TestRunJSON_NoMatchSessionAndAgent(t *testing.T) {
	t.Parallel()
	sessDir := setupTestSessions(t)
	var buf bytes.Buffer
	err := monitor.RunJSON(context.Background(), sessDir, "", "", "", "", 8760*time.Hour, "sess-1", "nonexistent-agent", &buf)
	if err == nil {
		t.Fatal("expected error for missing session+agent, got nil")
	}
	if !strings.Contains(err.Error(), "sess-1") {
		t.Errorf("error %q does not contain sid", err.Error())
	}
}
