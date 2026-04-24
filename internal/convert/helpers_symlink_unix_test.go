//go:build !windows

package convert

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/kapmcli/kapm/internal/fileutil"
)

func TestCopyFile_RefusesSymlinkSource(t *testing.T) {
	t.Parallel()

	srcDir := t.TempDir()
	dstDir := t.TempDir()

	target := filepath.Join(srcDir, "target.txt")
	if err := os.WriteFile(target, []byte("secret"), 0o644); err != nil {
		t.Fatalf("setup target: %v", err)
	}
	src := filepath.Join(srcDir, "link.txt")
	if err := os.Symlink(target, src); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	err := copyFile(src, filepath.Join(dstDir, "dst.txt"), 0o644)
	if err == nil {
		t.Fatal("copyFile() error = nil, want symlink refusal")
	}
	if !errors.Is(err, syscall.ELOOP) {
		t.Fatalf("error = %v, want ELOOP", err)
	}
}

func TestCopyDirectory_RefusesSymlinkDest(t *testing.T) {
	t.Parallel()

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("setup src: %v", err)
	}

	externalDir := t.TempDir()
	externalFile := filepath.Join(externalDir, "keep.txt")
	if err := os.WriteFile(externalFile, []byte("preserve"), 0o644); err != nil {
		t.Fatalf("setup external: %v", err)
	}

	parent := t.TempDir()
	dst := filepath.Join(parent, "dst")
	if err := os.Symlink(externalDir, dst); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	_, err := copyDirectory(srcDir, dst, true)
	if err == nil {
		t.Fatal("copyDirectory() error = nil, want symlink refusal")
	}
	if !strings.Contains(err.Error(), "refuse to remove symlink") {
		t.Fatalf("error = %q, want %q", err.Error(), "refuse to remove symlink")
	}

	// External target must be untouched.
	got, err := os.ReadFile(externalFile)
	if err != nil {
		t.Fatalf("ReadFile external: %v", err)
	}
	if string(got) != "preserve" {
		t.Fatalf("external content = %q, want %q", got, "preserve")
	}
}

func TestWriteFilePair_RefusesSymlinkDest(t *testing.T) {
	t.Parallel()

	externalDir := t.TempDir()
	external := filepath.Join(externalDir, "keep.txt")
	if err := os.WriteFile(external, []byte("preserve"), 0o644); err != nil {
		t.Fatalf("setup external: %v", err)
	}

	dir := t.TempDir()
	first := filepath.Join(dir, "first.txt")
	second := filepath.Join(dir, "second.txt")
	if err := os.Symlink(external, first); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	_, err := fileutil.WriteFilePair(first, []byte("a"), second, []byte("b"), true)
	if err == nil {
		t.Fatal("WriteFilePair() error = nil, want symlink refusal")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite symlink") {
		t.Fatalf("error = %q, want symlink refusal error", err.Error())
	}

	got, err := os.ReadFile(external)
	if err != nil {
		t.Fatalf("ReadFile external: %v", err)
	}
	if string(got) != "preserve" {
		t.Fatalf("external content = %q, want %q", got, "preserve")
	}
}
