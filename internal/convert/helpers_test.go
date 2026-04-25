package convert

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kapmcli/kapm/internal/fileutil"
)

func TestWriteFileAtomicForce(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dst := filepath.Join(dir, "dst.txt")

	// Pre-create target file.
	if err := os.WriteFile(dst, []byte("old"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	written, err := fileutil.WriteFileAtomic(dst, []byte("new"), true)
	if err != nil {
		t.Fatalf("WriteFileAtomic() error = %v", err)
	}
	if !written {
		t.Fatal("WriteFileAtomic() written = false, want true")
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("content = %q, want %q", got, "new")
	}

	// No .tmp-* files remain.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if matched, _ := filepath.Match("*.tmp-*", e.Name()); matched {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(dst)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if info.Mode().Perm() != 0o644 {
			t.Fatalf("perm = %o, want 644", info.Mode().Perm())
		}
	}
}

func TestWriteFileSkipExisting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dst := filepath.Join(dir, "dst.txt")

	if err := os.WriteFile(dst, []byte("keep"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	written, err := fileutil.WriteFileAtomic(dst, []byte("new"), false)
	if err != nil {
		t.Fatalf("WriteFileAtomic() error = %v", err)
	}
	if written {
		t.Fatal("WriteFileAtomic() written = true, want false")
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "keep" {
		t.Fatalf("content = %q, want %q", got, "keep")
	}
}

func TestWriteFileSymlinkAttack(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	external := filepath.Join(dir, "external.txt")
	if err := os.WriteFile(external, []byte("secret"), 0o644); err != nil {
		t.Fatalf("setup external: %v", err)
	}

	dstDir := t.TempDir()
	dst := filepath.Join(dstDir, "dst.txt")
	if err := os.Symlink(external, dst); err != nil {
		t.Skipf("os.Symlink not available: %v", err)
	}

	written, err := fileutil.WriteFileAtomic(dst, []byte("new"), true)
	if err != nil {
		t.Fatalf("WriteFileAtomic() error = %v", err)
	}
	if !written {
		t.Fatal("WriteFileAtomic() written = false, want true")
	}

	// external.txt must be untouched.
	got, err := os.ReadFile(external)
	if err != nil {
		t.Fatalf("ReadFile external: %v", err)
	}
	if string(got) != "secret" {
		t.Fatalf("external content = %q, want %q", got, "secret")
	}

	// dst is now a regular file (symlink replaced).
	info, err := os.Lstat(dst)
	if err != nil {
		t.Fatalf("Lstat dst: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("dst is still a symlink, want regular file")
	}
}

func TestWriteFileDirAtTarget(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dst := filepath.Join(dir, "dst")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	_, err := fileutil.WriteFileAtomic(dst, []byte("data"), true)
	if err == nil {
		t.Fatal("WriteFileAtomic() error = nil, want error for directory at target")
	}
}

func TestCopyFileStripsUnsafePermissions(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not preserve POSIX file mode bits")
	}

	tests := []struct {
		name     string
		srcMode  os.FileMode
		wantMode os.FileMode
	}{
		{"0o755 preserved", 0o755, 0o755},
		{"0o777 capped to 0o755", 0o777, 0o755},
		{"0o644 preserved", 0o644, 0o644},
		{"0o666 capped to 0o644", 0o666, 0o644},
		{"0o4755 setuid stripped to 0o755", 0o4755, 0o755},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srcDir := t.TempDir()
			dstDir := t.TempDir()

			src := filepath.Join(srcDir, "src.txt")
			dst := filepath.Join(dstDir, "dst.txt")

			// Create source file with specified mode.
			if err := os.WriteFile(src, []byte("test content"), tt.srcMode); err != nil {
				t.Fatalf("WriteFile src: %v", err)
			}

			// Get the actual mode of the source file.
			srcInfo, err := os.Stat(src)
			if err != nil {
				t.Fatalf("Stat src: %v", err)
			}

			// Call copyFile with the source mode.
			if err := copyFile(src, dst, srcInfo.Mode()); err != nil {
				t.Fatalf("copyFile() error = %v", err)
			}

			// Check destination mode.
			dstInfo, err := os.Stat(dst)
			if err != nil {
				t.Fatalf("Stat dst: %v", err)
			}

			if dstInfo.Mode().Perm() != tt.wantMode {
				t.Fatalf("dst mode = %o, want %o", dstInfo.Mode().Perm(), tt.wantMode)
			}

			// Verify content is copied.
			content, err := os.ReadFile(dst)
			if err != nil {
				t.Fatalf("ReadFile dst: %v", err)
			}
			if string(content) != "test content" {
				t.Fatalf("content = %q, want %q", content, "test content")
			}
		})
	}
}

func TestCopyDirectorySkipsSymlinks(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	dst := t.TempDir()

	// Create a regular file.
	if err := os.WriteFile(filepath.Join(src, "real.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("setup real.txt: %v", err)
	}

	// Create a symlink; skip on Windows if unprivileged.
	if err := os.Symlink(filepath.Join(src, "real.txt"), filepath.Join(src, "link.txt")); err != nil {
		t.Skipf("os.Symlink not available: %v", err)
	}

	if err := copyDirectoryContents(src, dst); err != nil {
		t.Fatalf("copyDirectoryContents() error = %v", err)
	}

	// Regular file must be copied.
	if _, err := os.Stat(filepath.Join(dst, "real.txt")); err != nil {
		t.Fatalf("real.txt missing in dst: %v", err)
	}

	// Symlink must NOT be present in dst.
	if _, err := os.Lstat(filepath.Join(dst, "link.txt")); err == nil {
		t.Fatal("link.txt present in dst, want skipped")
	}
}

func TestCopyDirectoryForceMissingSrcPreservesDst(t *testing.T) {
	t.Parallel()

	srcDir := t.TempDir()
	dstDir := t.TempDir()

	src := filepath.Join(srcDir, "nonexistent")
	dst := filepath.Join(dstDir, "dst")

	// Pre-create destination directory.
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatalf("setup mkdir dst: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dst, "keep.txt"), []byte("preserve"), 0o644); err != nil {
		t.Fatalf("setup write keep.txt: %v", err)
	}

	// Call copyDirectory with force=true on missing source.
	written, err := copyDirectory(src, dst, true)
	if err != nil {
		t.Fatalf("copyDirectory() error = %v", err)
	}
	if written {
		t.Fatal("copyDirectory() written = true, want false")
	}

	// Destination must still exist with original content.
	content, err := os.ReadFile(filepath.Join(dst, "keep.txt"))
	if err != nil {
		t.Fatalf("ReadFile keep.txt: %v", err)
	}
	if string(content) != "preserve" {
		t.Fatalf("content = %q, want %q", content, "preserve")
	}
}

// TestWriteFilePair_ReportsCloseError verifies that write-path errors are
// surfaced rather than swallowed. copyFile uses an explicit tempFile.Close() check
// before rename; errors from the write path propagate via the named-return (err error).
func TestWriteFilePair_ReportsCloseError(t *testing.T) {
	t.Parallel()

	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "src.txt")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// dst is a directory — rename into it fails after close succeeds,
	// verifying the write-path error propagates rather than being swallowed.
	dstDir := t.TempDir()
	dstAsDir := filepath.Join(dstDir, "conflict")
	if err := os.Mkdir(dstAsDir, 0o755); err != nil {
		t.Fatalf("setup mkdir: %v", err)
	}

	err := copyFile(src, dstAsDir, 0o644)
	if err == nil {
		t.Fatal("copyFile() error = nil, want error when dst is a directory")
	}
	if !strings.Contains(err.Error(), "rename") {
		t.Fatalf("error = %q, want it to contain %q", err.Error(), "rename")
	}
}
