package hook

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kapmcli/kapm/internal/testutil"
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

func waitForPathState(path string, wantExists bool) error {
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		_, err := os.Stat(path)
		if wantExists && err == nil {
			return nil
		}
		if !wantExists && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if time.Now().After(deadline) {
			if wantExists {
				return err
			}
			if err == nil {
				return errors.New("path still exists")
			}
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
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
	if _, err := os.Stat(filepath.Join(dir, "old.jsonl")); !errors.Is(err, fs.ErrNotExist) {
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
	if _, err := os.Stat(filepath.Join(dir, "curr.jsonl.gz")); !errors.Is(err, fs.ErrNotExist) {
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
	if runtime.GOOS == "windows" {
		t.Skip("Windows permits replacing a directory with os.Rename in this setup")
	}

	buf, restore := testutil.CaptureSlog(t)
	defer restore()

	dir := t.TempDir()
	content := `{"event":"x"}` + "\n"
	src := filepath.Join(dir, "old.jsonl")
	writeJSONL(t, src, content)

	// Simulate failure by making the tmp path unwritable via a directory with same name.
	tmp := filepath.Join(dir, "old.jsonl.gz.tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatalf("setup nested file: %v", err)
	}

	var stderr bytes.Buffer
	rotate(dir, "current", &stderr, 0)

	// tmp directory should still exist because it's non-empty and os.Remove(tmp)
	// cannot clear it during recovery, so the original .jsonl must be intact.
	if _, err := os.Stat(src); err != nil {
		t.Error("original .jsonl should be intact after failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "old.jsonl.gz")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("old.jsonl.gz should not exist after failure")
	}
	if !strings.Contains(buf.String(), "rotate compress") {
		t.Error("expected rotate compress error in slog output")
	}
}

func TestRotateRetriesAfterRestoredFailure(t *testing.T) {
	dir := t.TempDir()
	content := `{"event":"x"}` + "\n"
	src := filepath.Join(dir, "old.jsonl")
	tmp := filepath.Join(dir, "old.jsonl.gz.tmp")
	writeJSONL(t, src, content)

	calls := 0
	testHookAfterClaim = func() {
		calls++
		switch calls {
		case 1:
			if err := os.Mkdir(tmp, 0o700); err != nil {
				t.Fatalf("setup retry tmp dir: %v", err)
			}
		case 2:
			testHookAfterClaim = nil
		}
	}
	t.Cleanup(func() {
		testHookAfterClaim = nil
		_ = os.Remove(tmp)
	})

	var stderr bytes.Buffer
	rotate(dir, "current", &stderr, 0)

	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr after retry: %s", stderr.String())
	}
	if calls != 2 {
		t.Fatalf("expected 2 compression attempts, got %d", calls)
	}
	if _, err := os.Stat(src); !errors.Is(err, fs.ErrNotExist) {
		t.Error("old.jsonl should be removed after successful retry")
	}
	got := readGzip(t, src+".gz")
	if got != content {
		t.Errorf("gz content mismatch after retry: got %q want %q", got, content)
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
	if err := waitForPathState(filepath.Join(dir, "shared.jsonl.gz"), true); err != nil {
		t.Errorf("shared.jsonl.gz should exist: %v", err)
	}
	if err := waitForPathState(filepath.Join(dir, "shared.jsonl"), false); err != nil {
		t.Errorf("shared.jsonl should be removed: %v", err)
	}
	// No .tmp leftovers.
	if err := waitForPathState(filepath.Join(dir, "shared.jsonl.gz.tmp"), false); err != nil {
		t.Errorf("tmp file should not remain: %v", err)
	}
	if err := waitForPathState(filepath.Join(dir, "shared.jsonl.rotating"), false); err != nil {
		t.Errorf("rotating file should not remain: %v", err)
	}
	// Content intact.
	got := readGzip(t, filepath.Join(dir, "shared.jsonl.gz"))
	if got != content {
		t.Errorf("gz content mismatch")
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
	if _, err := os.Stat(filepath.Join(dir, "recent.jsonl.gz")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("recent.jsonl.gz should not be created")
	}
}

// TestCompressFile_RestoreJoinsErrors verifies that when the restore rename fails during
// recovery, the returned error contains both the primary cause and the restore failure.
func TestCompressFile_RestoreJoinsErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce POSIX directory write bits for this test")
	}
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

// TestRotateReadDirErrorLogsWarning verifies that an unexpected ReadDir error
// (not ErrNotExist) triggers slog.Warn with "read logs dir failed".
func TestRotateReadDirErrorLogsWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce POSIX directory write bits for this test")
	}
	if os.Getuid() == 0 {
		t.Skip("cannot test permission errors as root")
	}

	buf, restore := testutil.CaptureSlog(t)
	defer restore()

	parent := t.TempDir()
	logDir := filepath.Join(parent, "logs")
	if err := os.Mkdir(logDir, 0o000); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(logDir, 0o700) })

	var stderr bytes.Buffer
	rotate(logDir, "current", &stderr, 0)

	if !strings.Contains(buf.String(), "read logs dir failed") {
		t.Errorf("expected 'read logs dir failed' in slog output, got: %q", buf.String())
	}
}

// TestRotateCompressErrorWrapped verifies that a compression failure is logged via
// slog.Warn with the "rotate compress" prefix in the error message.
func TestRotateCompressErrorWrapped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce POSIX directory write bits for this test")
	}
	if os.Getuid() == 0 {
		t.Skip("cannot test permission errors as root")
	}

	buf, restore := testutil.CaptureSlog(t)
	defer restore()

	dir := t.TempDir()
	src := filepath.Join(dir, "old.jsonl")
	writeJSONL(t, src, `{"event":"x"}`+"\n")

	// Make the tmp path unwritable via a directory with the same name.
	tmp := filepath.Join(dir, "old.jsonl.gz.tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatalf("setup nested file: %v", err)
	}

	var stderr bytes.Buffer
	rotate(dir, "current", &stderr, 0)

	if !strings.Contains(buf.String(), "rotate compress") {
		t.Errorf("expected 'rotate compress' in slog output, got: %q", buf.String())
	}
}
