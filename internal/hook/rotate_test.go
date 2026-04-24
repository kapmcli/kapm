package hook

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// failReader is a fake io.Reader that always returns err.
type failReader struct{ err error }

func (f *failReader) Read(_ []byte) (int, error) { return 0, f.err }

func writeJSONL(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func readGzip(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open gz %q: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader %q: %v", path, err)
	}
	defer func() { _ = gr.Close() }()
	data, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("read gz %q: %v", path, err)
	}
	return string(data)
}

func TestRotateBasic(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat(`{"event":"x"}`+"\n", 5)
	writeJSONL(t, filepath.Join(dir, "old.jsonl"), content)

	var stderr bytes.Buffer
	rotate(dir, "current", &stderr, 0)

	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr: %s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "old.jsonl")); !os.IsNotExist(err) {
		t.Error("old.jsonl should be removed after rotation")
	}
	got := readGzip(t, filepath.Join(dir, "old.jsonl.gz"))
	if got != content {
		t.Errorf("gz content mismatch: got %q want %q", got, content)
	}
}

func TestRotateSkipsAlreadyCompressed(t *testing.T) {
	dir := t.TempDir()
	content := `{"event":"x"}` + "\n"
	writeJSONL(t, filepath.Join(dir, "old.jsonl"), content)

	var stderr bytes.Buffer
	rotate(dir, "current", &stderr, 0)

	// Get mtime of .gz
	info1, err := os.Stat(filepath.Join(dir, "old.jsonl.gz"))
	if err != nil {
		t.Fatalf("gz not created: %v", err)
	}

	// Run rotate again — should not reprocess
	rotate(dir, "current", &stderr, 0)

	info2, err := os.Stat(filepath.Join(dir, "old.jsonl.gz"))
	if err != nil {
		t.Fatalf("gz gone after second rotate: %v", err)
	}
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Error("gz mtime changed — file was reprocessed")
	}
}

func TestRotateCurrentSessionNotRotated(t *testing.T) {
	dir := t.TempDir()
	content := `{"event":"x"}` + "\n"
	writeJSONL(t, filepath.Join(dir, "curr.jsonl"), content)

	var stderr bytes.Buffer
	rotate(dir, "curr", &stderr, 0)

	if _, err := os.Stat(filepath.Join(dir, "curr.jsonl")); err != nil {
		t.Error("curr.jsonl should still exist")
	}
	if _, err := os.Stat(filepath.Join(dir, "curr.jsonl.gz")); !os.IsNotExist(err) {
		t.Error("curr.jsonl.gz should not be created")
	}
}

func TestRotateNoPriorFiles(t *testing.T) {
	dir := t.TempDir()
	var stderr bytes.Buffer
	rotate(dir, "first", &stderr, 0)
	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr: %s", stderr.String())
	}
}

func TestRotateFailureRecovery(t *testing.T) {
	dir := t.TempDir()
	content := `{"event":"x"}` + "\n"
	src := filepath.Join(dir, "old.jsonl")
	writeJSONL(t, src, content)

	// Simulate failure by making the tmp path unwritable via a directory with same name.
	tmp := filepath.Join(dir, "old.jsonl.gz.tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var stderr bytes.Buffer
	rotate(dir, "current", &stderr, 0)

	// tmp directory should still exist (we can't remove a dir with os.Remove), but
	// the original .jsonl must be intact.
	if _, err := os.Stat(src); err != nil {
		t.Error("original .jsonl should be intact after failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "old.jsonl.gz")); !os.IsNotExist(err) {
		t.Error("old.jsonl.gz should not exist after failure")
	}
	if stderr.Len() == 0 {
		t.Error("expected error on stderr")
	}
}

func TestRotateConcurrentNoDoubleProcess(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat(`{"event":"x"}`+"\n", 100)
	writeJSONL(t, filepath.Join(dir, "shared.jsonl"), content)

	var wg sync.WaitGroup
	const n = 5
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			var stderr bytes.Buffer
			rotate(dir, "current", &stderr, 0)
		}()
	}
	wg.Wait()

	// Exactly one .gz should exist, original removed.
	if _, err := os.Stat(filepath.Join(dir, "shared.jsonl.gz")); err != nil {
		t.Error("shared.jsonl.gz should exist")
	}
	if _, err := os.Stat(filepath.Join(dir, "shared.jsonl")); !os.IsNotExist(err) {
		t.Error("shared.jsonl should be removed")
	}
	// No .tmp leftovers.
	if _, err := os.Stat(filepath.Join(dir, "shared.jsonl.gz.tmp")); !os.IsNotExist(err) {
		t.Error("tmp file should not remain")
	}
	// Content intact.
	got := readGzip(t, filepath.Join(dir, "shared.jsonl.gz"))
	if got != content {
		t.Errorf("gz content mismatch")
	}
}

// TestHandleRotatesOnAgentSpawn verifies the integration: Handle calls rotate after
// writing the agentSpawn record.
func TestHandleRotatesOnAgentSpawn(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, ".kiro", "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldContent := `{"event":"x"}` + "\n"
	oldPath := filepath.Join(logDir, "old.jsonl")
	writeJSONL(t, oldPath, oldContent)
	// Set mtime to 48h ago so it passes the 24h threshold.
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldPath, old, old); err != nil {
		t.Fatalf("chtimes %q: %v", oldPath, err)
	}

	in := strings.NewReader(`{"hook_event_name":"agentSpawn","session_id":"new","cwd":"/tmp"}`)
	code := Handle(in, &bytes.Buffer{}, &bytes.Buffer{}, fixedNow, dir, "a")
	if code != 0 {
		t.Fatalf("want 0, got %d", code)
	}

	if _, err := os.Stat(filepath.Join(logDir, "old.jsonl")); !os.IsNotExist(err) {
		t.Error("old.jsonl should be rotated away")
	}
	if _, err := os.Stat(filepath.Join(logDir, "old.jsonl.gz")); err != nil {
		t.Error("old.jsonl.gz should exist")
	}
	// Current session file must still exist.
	if _, err := os.Stat(filepath.Join(logDir, "new.jsonl")); err != nil {
		t.Error("new.jsonl should exist")
	}
}

func TestRotateSkipsRecentFiles(t *testing.T) {
	dir := t.TempDir()
	content := `{"event":"x"}` + "\n"
	writeJSONL(t, filepath.Join(dir, "recent.jsonl"), content)
	// mtime is now (just written) — should be skipped with 24h threshold.

	var stderr bytes.Buffer
	rotate(dir, "current", &stderr, 24*time.Hour)

	if _, err := os.Stat(filepath.Join(dir, "recent.jsonl")); err != nil {
		t.Error("recent.jsonl should still exist (mtime too recent)")
	}
	if _, err := os.Stat(filepath.Join(dir, "recent.jsonl.gz")); !os.IsNotExist(err) {
		t.Error("recent.jsonl.gz should not be created")
	}
}

// TestCompressFile_RestoreJoinsErrors verifies that when the restore rename fails during
// recovery, the returned error contains both the primary cause and the restore failure.
func TestCompressFile_RestoreJoinsErrors(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("cannot test permission errors as root")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "test.jsonl")
	writeJSONL(t, src, `{"event":"x"}`+"\n")
	dst := src + ".gz"

	// After the initial rename claims the file, make the directory read-only so that
	// writeGzip cannot create the tmp file and the restore rename also fails.
	testHookAfterClaim = func() {
		testHookAfterClaim = nil
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Errorf("chmod: %v", err)
		}
	}
	t.Cleanup(func() {
		testHookAfterClaim = nil
		_ = os.Chmod(dir, 0o700)
	})

	err := compressFile(src, dst)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "restore") {
		t.Errorf("error should mention restore, got: %v", err)
	}
}

// failWriteCloser is a fake io.WriteCloser that always succeeds on Write and returns
// closeErr on Close.
type failWriteCloser struct {
	closeErr error
}

func (f *failWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (f *failWriteCloser) Close() error                { return f.closeErr }

// TestWriteGzipTo_CopyFailJoinsCloseErrors verifies that when io.Copy fails, the
// returned error contains both the copy error and the out.Close() error.
func TestWriteGzipTo_CopyFailJoinsCloseErrors(t *testing.T) {
	readErr := errors.New("read fail")
	r := &failReader{err: readErr}
	out := &failWriteCloser{closeErr: errors.New("out close fail")}

	err := writeGzipTo(r, out)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, readErr) {
		t.Errorf("expected read error in chain, got: %v", err)
	}
	if !strings.Contains(err.Error(), "out close fail") {
		t.Errorf("expected out close error in message, got: %v", err)
	}
}

// TestWriteGzipTo_GzCloseFailJoinsOutClose verifies that when gw.Close() succeeds but
// out.Close() fails, the error is returned.
func TestWriteGzipTo_GzCloseFailJoinsOutClose(t *testing.T) {
	r := strings.NewReader("hello")
	out := &failWriteCloser{closeErr: errors.New("out close fail")}

	err := writeGzipTo(r, out)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "out close fail") {
		t.Errorf("expected out close error in message, got: %v", err)
	}
}

// TestHandleNoRotateOnOtherEvents verifies rotate is NOT called for non-agentSpawn events.
func TestHandleNoRotateOnOtherEvents(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, ".kiro", "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldContent := `{"event":"x"}` + "\n"
	writeJSONL(t, filepath.Join(logDir, "old.jsonl"), oldContent)

	in := strings.NewReader(`{"hook_event_name":"postToolUse","session_id":"new","cwd":"/tmp"}`)
	Handle(in, &bytes.Buffer{}, &bytes.Buffer{}, fixedNow, dir, "a")

	if _, err := os.Stat(filepath.Join(logDir, "old.jsonl")); err != nil {
		t.Error("old.jsonl should NOT be rotated for non-agentSpawn events")
	}
}
