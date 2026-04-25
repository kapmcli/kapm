package testutil

import (
	"bytes"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// CaptureSlog replaces the default slog.Logger with a text-handler writing
// to a buffer, returns the buffer and a restore func.
//
// Not safe for t.Parallel(): slog.SetDefault mutates process-global state.
// Tests using CaptureSlog must not call t.Parallel() and must not be run
// concurrently with other goroutines that emit via slog.Default().
func CaptureSlog(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	return &buf, func() { slog.SetDefault(prev) }
}

// CopyDir recursively copies src into dst for test setup.
func CopyDir(t testing.TB, src, dst string) {
	t.Helper()

	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", src, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dst, err)
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			CopyDir(t, srcPath, dstPath)
			continue
		}

		data, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", srcPath, err)
		}
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("Info(%q): %v", srcPath, err)
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", filepath.Dir(dstPath), err)
		}
		if err := os.WriteFile(dstPath, data, info.Mode().Perm()); err != nil {
			t.Fatalf("WriteFile(%q): %v", dstPath, err)
		}
	}
}

// AssertDirEqual fails the test if two directory trees differ in files or contents.
func AssertDirEqual(t testing.TB, gotDir, wantDir string) {
	t.Helper()

	gotFiles := listFiles(t, gotDir)
	wantFiles := listFiles(t, wantDir)

	if len(gotFiles) != len(wantFiles) {
		t.Fatalf("file count mismatch: got %d files, want %d\ngot: %v\nwant: %v", len(gotFiles), len(wantFiles), gotFiles, wantFiles)
	}

	for i := range wantFiles {
		if gotFiles[i] != wantFiles[i] {
			t.Fatalf("file list mismatch at %d: got %q, want %q\ngot: %v\nwant: %v", i, gotFiles[i], wantFiles[i], gotFiles, wantFiles)
		}

		gotData, err := os.ReadFile(filepath.Join(gotDir, gotFiles[i]))
		if err != nil {
			t.Fatalf("ReadFile(got %q): %v", gotFiles[i], err)
		}
		wantData, err := os.ReadFile(filepath.Join(wantDir, wantFiles[i]))
		if err != nil {
			t.Fatalf("ReadFile(want %q): %v", wantFiles[i], err)
		}
		gotText := normalizeTextForCompare(string(gotData))
		wantText := normalizeTextForCompare(string(wantData))
		if gotText != wantText {
			t.Fatalf("content mismatch for %q\n\n--- got ---\n%s\n--- want ---\n%s", gotFiles[i], gotData, wantData)
		}
	}
}

func normalizeTextForCompare(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

func listFiles(t testing.TB, root string) []string {
	t.Helper()

	files := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%q): %v", root, err)
	}
	sort.Strings(files)
	return files
}
