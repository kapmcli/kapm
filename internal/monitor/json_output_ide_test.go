package monitor_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/monitor"
)

// buildIDEDir creates a minimal IDE directory with one session and one execution log.
func buildIDEDir(t *testing.T) string {
	t.Helper()
	ideDir := t.TempDir()
	wsName := "L2hvbWUvdXNlci9wcm9qZWN0LWFscGhh" // base64url(/home/user/project-alpha)
	wsDir := filepath.Join(ideDir, "workspace-sessions", wsName)
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionsJSON := `[{"sessionId":"711eb717-0000-0000-0000-000000000001","title":"Implement feature X","dateCreated":"1777435222255","workspaceDirectory":"/home/user/project-alpha"}]`
	if err := os.WriteFile(filepath.Join(wsDir, "sessions.json"), []byte(sessionsJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionHistory := `{"history":[{"message":{"role":"assistant","content":"done"},"executionId":"c8a800f5-0000-0000-0000-000000000001"}]}`
	if err := os.WriteFile(filepath.Join(wsDir, "711eb717-0000-0000-0000-000000000001.json"), []byte(sessionHistory), 0o644); err != nil {
		t.Fatal(err)
	}

	// Execution log under a fake profile hash directory.
	const profileHash = "be6800b174065600d20a690aaef89855"
	sessionHashDir := filepath.Join(ideDir, profileHash, "aabbccdd11223344aabbccdd11223344")
	if err := os.MkdirAll(sessionHashDir, 0o755); err != nil {
		t.Fatal(err)
	}
	execLogJSON := `{"executionId":"c8a800f5-0000-0000-0000-000000000001","chatSessionId":"711eb717-0000-0000-0000-000000000001","startTime":1777435222200,"endTime":1777435250000,"usageSummary":[{"unit":"credit","usage":0.125}]}`
	if err := os.WriteFile(filepath.Join(sessionHashDir, "execlog1"), []byte(execLogJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return ideDir
}

// TestRunJSON_IDESession verifies that RunJSON includes IDE sessions in JSON output.
func TestRunJSON_IDESession(t *testing.T) {
	t.Parallel()
	sessDir := setupTestSessions(t)
	ideDir := buildIDEDir(t)

	var buf bytes.Buffer
	if err := monitor.RunJSON(context.Background(), sessDir, "", ideDir, "", "", 8760*time.Hour, "", "", &buf); err != nil {
		t.Fatalf("RunJSON: %v", err)
	}
	var dm monitor.DetailedMetrics
	if err := json.Unmarshal(buf.Bytes(), &dm); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	var found bool
	for _, s := range dm.Sessions {
		if s.Agent == "kiro-ide" {
			found = true
			if s.Duration <= 0 {
				t.Errorf("IDE session Duration: want > 0, got %v", s.Duration)
			}
			break
		}
	}
	if !found {
		t.Error("expected kiro-ide session in JSON output, not found")
	}
}

// TestRunJSON_AgentFilter_IDE verifies that --agent kiro-ide returns only IDE sessions.
func TestRunJSON_AgentFilter_IDE(t *testing.T) {
	t.Parallel()
	sessDir := setupTestSessions(t)
	ideDir := buildIDEDir(t)

	var buf bytes.Buffer
	if err := monitor.RunJSON(context.Background(), sessDir, "", ideDir, "", "", 8760*time.Hour, "", "kiro-ide", &buf); err != nil {
		t.Fatalf("RunJSON: %v", err)
	}
	var dm monitor.DetailedMetrics
	if err := json.Unmarshal(buf.Bytes(), &dm); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	for _, s := range dm.Sessions {
		if s.Agent != "kiro-ide" {
			t.Errorf("expected only kiro-ide sessions, got agent=%q", s.Agent)
		}
	}
	if len(dm.Sessions) == 0 {
		t.Error("expected at least one kiro-ide session")
	}
}
