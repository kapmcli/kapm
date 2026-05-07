package idehookhandler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHandleWritesObservedMinimalIDEHookRecord(t *testing.T) {
	root := t.TempDir()
	var stderr bytes.Buffer
	err := Handle(Options{
		Root:  root,
		Event: "fileEdited",
		Agent: "ide",
		Err:   &stderr,
		Now: func() time.Time {
			return time.Date(2026, 5, 3, 1, 2, 3, 4, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	logPath := filepath.Join(root, ".kapm", "logs", "ide", "events.jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(log): %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &rec); err != nil {
		t.Fatalf("Unmarshal(log): %v", err)
	}
	for _, field := range []string{"stdin", "stdin_bytes", "env", "env_keys", "prompt", "toolResult", "tool_input", "tool_response", "session", "tool"} {
		if _, ok := rec[field]; ok {
			t.Fatalf("field %q must not be logged: %#v", field, rec)
		}
	}
	if rec["event"] != "fileEdited" || rec["agent"] != "ide" {
		t.Fatalf("record = %#v", rec)
	}
	if rec["cwd"] == "" {
		t.Fatalf("cwd should be recorded from the hook process working directory: %#v", rec)
	}
}

func TestHandleRejectsSymlinkedNestedLogPath(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kapm"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ".kapm", "logs")); err != nil {
		t.Skipf("os.Symlink not available: %v", err)
	}

	err := Handle(Options{Root: root, Event: "fileEdited", Agent: "ide"})
	if err == nil {
		t.Fatal("Handle() error = nil, want symlink refusal")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("is a symlink")) {
		t.Fatalf("Handle() error = %v, want symlink refusal", err)
	}
	if _, err := os.Stat(filepath.Join(target, "ide")); err == nil {
		t.Fatal("IDE log dir should not have been created under symlink target")
	}
}

func TestHandleRejectsSymlinkedLogFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "events-target.jsonl")
	if err := os.WriteFile(target, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	logDir := filepath.Join(root, ".kapm", "logs", "ide")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(logDir, "events.jsonl")); err != nil {
		t.Skipf("os.Symlink not available: %v", err)
	}

	err := Handle(Options{Root: root, Event: "fileEdited", Agent: "ide"})
	if err == nil {
		t.Fatal("Handle() error = nil, want symlink refusal")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("is a symlink")) {
		t.Fatalf("Handle() error = %v, want symlink refusal", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original\n" {
		t.Fatalf("symlink target was modified: %q", string(data))
	}
}

func TestHandle_ConcurrentAppend(t *testing.T) {
	root := t.TempDir()
	const workers, perWorker = 10, 10
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range perWorker {
				_ = Handle(Options{
					Root:   root,
					Event:  "PostToolUse",
					Agent:  fmt.Sprintf("agent-%d-%d", i, j),
					Now:    func() time.Time { return time.Now() },
					Getenv: func(string) string { return "" },
					Err:    io.Discard,
				})
			}
		}()
	}
	wg.Wait()

	data, err := os.ReadFile(filepath.Join(root, ".kapm", "logs", "ide", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != workers*perWorker {
		t.Fatalf("want %d lines, got %d", workers*perWorker, len(lines))
	}
	for i, line := range lines {
		var rec record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d invalid JSON: %v", i, err)
		}
	}
}
