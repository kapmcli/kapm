//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoggerWritesJSONL verifies `kapm hook-handler` consumes a hook event
// on stdin, writes a single JSONL record under .kiro/logs/<session>.jsonl,
// exits 0, and emits nothing on stdout.
func TestLoggerWritesJSONL(t *testing.T) {
	kapm := binary(t)
	root := t.TempDir()

	event := `{"hook_event_name":"preToolUse","session_id":"e2e-1","cwd":"/w","tool_name":"bash","tool_input":{"command":"echo hi"}}`

	cmd := exec.Command(kapm, "hook-handler")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "AGENT=e2e")
	cmd.Stdin = strings.NewReader(event)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("kapm hook-handler failed: %v\nstderr: %s", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("kapm hook-handler wrote to stdout: %q", stdout.String())
	}

	logPath := filepath.Join(root, ".kiro", "logs", "e2e-1.jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSONL line, got %d", len(lines))
	}

	var rec struct {
		Ts      string `json:"ts"`
		Agent   string `json:"agent"`
		Session string `json:"session"`
		Event   string `json:"event"`
		Tool    string `json:"tool"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("invalid JSONL: %v\nline: %s", err, lines[0])
	}
	if rec.Session != "e2e-1" || rec.Event != "preToolUse" || rec.Tool != "bash" || rec.Agent != "e2e" {
		t.Fatalf("unexpected record: %+v", rec)
	}
	if rec.Ts == "" {
		t.Fatal("ts missing")
	}
}

func TestLoggerAgentFlagWritesAgent(t *testing.T) {
	kapm := binary(t)
	root := t.TempDir()

	event := `{"hook_event_name":"preToolUse","session_id":"e2e-agent-flag","cwd":"/w","tool_name":"bash"}`
	cmd := exec.Command(kapm, "hook-handler", "--agent", "flag-agent")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "AGENT=env-agent")
	cmd.Stdin = strings.NewReader(event)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("kapm hook-handler --agent failed: %v\nstderr: %s", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("kapm hook-handler wrote to stdout: %q", stdout.String())
	}

	logPath := filepath.Join(root, ".kiro", "logs", "e2e-agent-flag.jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var rec struct {
		Agent string `json:"agent"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &rec); err != nil {
		t.Fatalf("invalid JSONL: %v\nline: %s", err, data)
	}
	if rec.Agent != "flag-agent" {
		t.Fatalf("agent = %q, want flag-agent", rec.Agent)
	}
}

// TestLoggerRejectsInvalidJSONExitsZero verifies the hook handler never breaks the
// host: invalid stdin still yields exit 0 and writes no log file, only stderr.
func TestLoggerRejectsInvalidJSONExitsZero(t *testing.T) {
	kapm := binary(t)
	root := t.TempDir()

	cmd := exec.Command(kapm, "hook-handler")
	cmd.Dir = root
	cmd.Stdin = strings.NewReader("not json")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("expected exit 0, got: %v\nstderr: %s", err, stderr.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("expected diagnostic on stderr")
	}
	if _, err := os.Stat(filepath.Join(root, ".kiro", "logs")); err == nil {
		t.Fatal("logs dir should not exist on invalid input")
	}
}
