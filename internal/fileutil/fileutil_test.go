package fileutil_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kapmcli/kapm/internal/fileutil"
	"github.com/kapmcli/kapm/internal/testutil"
)

func TestWarnIfKiroSymlink_Symlink(t *testing.T) {
	buf, restore := testutil.CaptureSlog(t)
	defer restore()
	dir := t.TempDir()
	target := filepath.Join(dir, "real-kiro")
	link := filepath.Join(dir, ".kiro")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("os.Symlink not available: %v", err)
	}
	fileutil.WarnIfKiroSymlink(filepath.Join(link, "logs"))
	if !strings.Contains(buf.String(), "symlink") {
		t.Errorf("expected slog warn about symlink, got: %s", buf.String())
	}
}

func TestWarnIfKiroSymlink_Regular(t *testing.T) {
	buf, restore := testutil.CaptureSlog(t)
	defer restore()
	dir := t.TempDir()
	kiroDir := filepath.Join(dir, ".kiro")
	if err := os.MkdirAll(filepath.Join(kiroDir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	fileutil.WarnIfKiroSymlink(filepath.Join(kiroDir, "logs"))
	if strings.Contains(buf.String(), "symlink") {
		t.Errorf("expected no warn, got: %s", buf.String())
	}
}

func TestWarnIfKiroSymlink_Missing(t *testing.T) {
	buf, restore := testutil.CaptureSlog(t)
	defer restore()
	fileutil.WarnIfKiroSymlink(filepath.Join(t.TempDir(), "nowhere", "logs"))
	if strings.Contains(buf.String(), "symlink") {
		t.Errorf("expected no warn for missing path, got: %s", buf.String())
	}
}

func TestWriteFileAtomic_Write(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	written, err := fileutil.WriteFileAtomic(path, []byte("hello"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Fatal("expected written=true")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestWriteFileAtomic_ForceOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	written, err := fileutil.WriteFileAtomic(path, []byte("new"), true)
	if err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Fatal("expected written=true")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Fatalf("got %q", got)
	}
}

func TestWriteFileAtomic_NonForceSkip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	written, err := fileutil.WriteFileAtomic(path, []byte("new"), false)
	if err != nil {
		t.Fatal(err)
	}
	if written {
		t.Fatal("expected written=false")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "existing" {
		t.Fatalf("file was modified: got %q", got)
	}
}

func TestWriteFileAtomic_ParentDirCreation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "out.txt")
	written, err := fileutil.WriteFileAtomic(path, []byte("data"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Fatal("expected written=true")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "data" {
		t.Fatalf("got %q", got)
	}
}

func TestIsSymlinkPath_Symlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("os.Symlink not available: %v", err)
	}
	got, err := fileutil.IsSymlinkPath(link)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !got {
		t.Error("want true, got false")
	}
}

func TestIsSymlinkPath_Regular(t *testing.T) {
	f := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := fileutil.IsSymlinkPath(f)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got {
		t.Error("want false, got true")
	}
}

func TestIsSymlinkPath_Missing(t *testing.T) {
	_, err := fileutil.IsSymlinkPath(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Error("want err, got nil")
	}
}

func TestIsSymlinkMode(t *testing.T) {
	if !fileutil.IsSymlinkMode(os.ModeSymlink) {
		t.Error("want true for ModeSymlink")
	}
	if fileutil.IsSymlinkMode(0) {
		t.Error("want false for 0")
	}
	if fileutil.IsSymlinkMode(os.ModeDir) {
		t.Error("want false for ModeDir")
	}
}

func TestWriteFilePair_Basic(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	written, err := fileutil.WriteFilePair(a, []byte("aaa"), b, []byte("bbb"), true)
	if err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Fatal("expected written=true")
	}
	if got, _ := os.ReadFile(a); string(got) != "aaa" {
		t.Fatalf("a: got %q", got)
	}
	if got, _ := os.ReadFile(b); string(got) != "bbb" {
		t.Fatalf("b: got %q", got)
	}
}

func TestWriteFilePair_ExistingSkipWithoutForce(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(a, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	written, err := fileutil.WriteFilePair(a, []byte("new"), b, []byte("bbb"), false)
	if err != nil {
		t.Fatal(err)
	}
	if written {
		t.Fatal("expected written=false")
	}
	if got, _ := os.ReadFile(a); string(got) != "original" {
		t.Fatalf("a was modified: got %q", got)
	}
	if _, err := os.Stat(b); err == nil {
		t.Fatal("b should not exist")
	}
}

func TestWriteFilePair_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(a, []byte("old-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("old-b"), 0o644); err != nil {
		t.Fatal(err)
	}
	written, err := fileutil.WriteFilePair(a, []byte("new-a"), b, []byte("new-b"), true)
	if err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Fatal("expected written=true")
	}
	if got, _ := os.ReadFile(a); string(got) != "new-a" {
		t.Fatalf("a: got %q", got)
	}
	if got, _ := os.ReadFile(b); string(got) != "new-b" {
		t.Fatalf("b: got %q", got)
	}
}

func TestWriteFilePair_RejectsSymlinkDest(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, a); err != nil {
		t.Skipf("os.Symlink not available: %v", err)
	}
	written, err := fileutil.WriteFilePair(a, []byte("data"), b, []byte("data"), true)
	if err == nil {
		t.Fatal("expected error for symlink dest")
	}
	if written {
		t.Fatal("expected written=false")
	}
	if _, err := os.Stat(b); err == nil {
		t.Fatal("b should not have been written")
	}
}

func TestWriteFilePair_RollbackPreservesOriginalMetadata(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	// b is in a non-existent subdirectory so the write will fail
	b := filepath.Join(dir, "nonexistent", "b.txt")
	if err := os.WriteFile(a, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	written, err := fileutil.WriteFilePair(a, []byte("new"), b, []byte("bbb"), true)
	if err == nil {
		t.Fatal("expected error")
	}
	if written {
		t.Fatal("expected written=false")
	}
	// pathA must be restored with original content and mode on POSIX filesystems.
	got, readErr := os.ReadFile(a)
	if readErr != nil {
		t.Fatalf("a missing after rollback: %v", readErr)
	}
	if string(got) != "original" {
		t.Fatalf("a content after rollback: got %q", got)
	}
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(a)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("a mode after rollback: got %o", info.Mode().Perm())
		}
	}
}
