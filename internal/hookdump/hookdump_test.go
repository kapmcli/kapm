package hookdump

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDumpWritesRawInputAndSelectedEnv(t *testing.T) {
	root := t.TempDir()
	t.Setenv("USER_PROMPT", "hello prompt")
	t.Setenv("KIRO_TEST_VALUE", "kiro-value")
	t.Setenv("SECRET_TOKEN", "must-not-log")

	var out strings.Builder
	err := Dump(Options{
		Root:  root,
		Event: "preToolUse",
		Agent: "ide",
		In:    strings.NewReader(`{"tool":"x"}`),
		Out:   &out,
		Now: func() time.Time {
			return time.Date(2026, 5, 3, 1, 2, 3, 4, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("Dump() error = %v", err)
	}

	logPath := filepath.Join(root, ".kapm", "logs", "hook-input.jsonl")
	if !strings.Contains(out.String(), logPath) {
		t.Fatalf("out = %q, want log path %q", out.String(), logPath)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(log): %v", err)
	}
	var rec record
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("Unmarshal(log): %v", err)
	}
	if rec.Event != "preToolUse" || rec.Agent != "ide" {
		t.Fatalf("record event/agent = %q/%q", rec.Event, rec.Agent)
	}
	if rec.Stdin != `{"tool":"x"}` || rec.StdinBytes != len(`{"tool":"x"}`) {
		t.Fatalf("stdin = %q (%d bytes)", rec.Stdin, rec.StdinBytes)
	}
	if rec.Env["USER_PROMPT"] != "hello prompt" || rec.Env["KIRO_TEST_VALUE"] != "kiro-value" {
		t.Fatalf("env = %#v", rec.Env)
	}
	if _, ok := rec.Env["SECRET_TOKEN"]; ok {
		t.Fatalf("SECRET_TOKEN should not be logged: %#v", rec.Env)
	}
	if !contains(rec.EnvKeys, "SECRET_TOKEN") {
		t.Fatalf("env_keys should include names only: %#v", rec.EnvKeys)
	}
}

type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) {
	select {}
}

func TestDumpDoesNotBlockWhenStdinNeverCloses(t *testing.T) {
	root := t.TempDir()

	start := time.Now()
	err := Dump(Options{
		Root:  root,
		Event: "stop",
		Agent: "ide",
		In:    blockingReader{},
		Out:   io.Discard,
		Now: func() time.Time {
			return time.Date(2026, 5, 3, 1, 2, 3, 4, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("Dump() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Dump() blocked for %s", elapsed)
	}

	data, err := os.ReadFile(filepath.Join(root, ".kapm", "logs", "hook-input.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile(log): %v", err)
	}
	var rec record
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("Unmarshal(log): %v", err)
	}
	if !rec.StdinReadTimed || rec.Stdin != "" || rec.StdinBytes != 0 {
		t.Fatalf("stdin timeout record = %#v", rec)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
