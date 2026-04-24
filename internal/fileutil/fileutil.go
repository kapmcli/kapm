package fileutil

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path atomically via a temp file + rename.
// If force is false and path already exists, returns (false, nil) without writing.
// Returns (true, nil) on successful write.
func WriteFileAtomic(path string, data []byte, force bool) (written bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("mkdir %q: %w", filepath.Dir(path), err)
	}

	if !force {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if errors.Is(err, fs.ErrExist) {
				return false, nil
			}
			return false, fmt.Errorf("write %q: %w", path, err)
		}
		defer func() {
			if closeErr := file.Close(); closeErr != nil && err == nil {
				err = fmt.Errorf("close %q: %w", path, closeErr)
			}
		}()
		if _, err := file.Write(data); err != nil {
			return false, fmt.Errorf("write %q: %w", path, err)
		}
		return true, nil
	}

	tempFile, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return false, fmt.Errorf("create temp %q: %w", path, err)
	}
	tempPath := tempFile.Name()
	defer func() {
		if removeErr := os.Remove(tempPath); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) && err == nil {
			err = fmt.Errorf("remove temp %q: %w", tempPath, removeErr)
		}
	}()

	if _, err := tempFile.Write(data); err != nil {
		return false, errors.Join(fmt.Errorf("write temp %q: %w", tempPath, err), tempFile.Close())
	}
	if err := tempFile.Chmod(0o644); err != nil {
		return false, errors.Join(fmt.Errorf("chmod temp %q: %w", tempPath, err), tempFile.Close())
	}
	if err := tempFile.Close(); err != nil {
		return false, fmt.Errorf("close temp %q: %w", tempPath, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return false, fmt.Errorf("rename %q: %w", path, err)
	}
	return true, nil
}

// writeTempFile creates a temp file in the same directory as path, writes data
// to it with mode 0o644, and returns the temp file path. The parent directory
// must already exist.
func writeTempFile(path string, data []byte) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create temp %q: %w", path, err)
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("write temp %q: %w", path, err)
	}
	if err := f.Chmod(0o644); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("chmod temp %q: %w", path, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("close temp %q: %w", path, err)
	}
	return tmp, nil
}

// WriteFilePair writes two files atomically as a pair: both files end up
// present on disk or neither is modified.
//
// If force is false and either path already exists, returns (false, nil)
// without writing anything.
//
// If force is true, both paths are overwritten via temp+rename. Symlinks at
// either destination are rejected before any write to prevent TOCTOU attacks.
// Both files are written with mode 0o644.
//
// Rollback: if pathB's write fails after pathA has already been replaced,
// pathA is restored from a rename-to-sibling backup so that its original
// content, mode, and ownership are preserved.
func WriteFilePair(pathA string, dataA []byte, pathB string, dataB []byte, force bool) (bool, error) {
	if !force {
		if _, err := os.Stat(pathA); err == nil {
			return false, nil
		}
		if _, err := os.Stat(pathB); err == nil {
			return false, nil
		}
	} else {
		for _, p := range []string{pathA, pathB} {
			isLink, err := IsSymlinkPath(p)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return false, fmt.Errorf("lstat %q: %w", p, err)
			}
			if isLink {
				return false, fmt.Errorf("refusing to overwrite symlink: %s", p)
			}
		}
	}

	tempA, err := writeTempFile(pathA, dataA)
	if err != nil {
		return false, err
	}
	tempB, err := writeTempFile(pathB, dataB)
	if err != nil {
		_ = os.Remove(tempA)
		return false, err
	}

	// Backup pathA by renaming to a sibling so rollback preserves metadata.
	backupA := pathA + ".kapm-bak"
	backupErr := os.Rename(pathA, backupA)
	hadA := backupErr == nil
	if backupErr != nil && !errors.Is(backupErr, fs.ErrNotExist) {
		_ = os.Remove(tempA)
		_ = os.Remove(tempB)
		return false, fmt.Errorf("backup %q: %w", pathA, backupErr)
	}

	if err := os.Rename(tempA, pathA); err != nil {
		if hadA {
			_ = os.Rename(backupA, pathA)
		}
		_ = os.Remove(tempA)
		_ = os.Remove(tempB)
		return false, fmt.Errorf("rename %q: %w", pathA, err)
	}
	if err := os.Rename(tempB, pathB); err != nil {
		// Roll back pathA.
		_ = os.Remove(pathA)
		if hadA {
			_ = os.Rename(backupA, pathA)
		}
		_ = os.Remove(tempB)
		return false, fmt.Errorf("rename %q: %w", pathB, err)
	}

	if hadA {
		_ = os.Remove(backupA)
	}
	return true, nil
}

// IsSymlinkPath reports whether path is a symlink.
// Returns (false, err) if path does not exist or Lstat fails.
// Returns (true, nil) if path exists and is a symlink.
// Returns (false, nil) if path exists but is not a symlink.
func IsSymlinkPath(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	return info.Mode()&os.ModeSymlink != 0, nil
}

// IsSymlinkMode reports whether m has the symlink bit set.
// Useful for inspecting os.FileMode returned by fs.DirEntry.Type() inside
// directory traversals where re-lstatting would be wasteful.
func IsSymlinkMode(m os.FileMode) bool {
	return m&os.ModeSymlink != 0
}

// WarnIfKiroSymlink emits a slog.Warn if the parent .kiro directory of
// logsDir is a symlink. It never returns an error (warn-only contract).
// Silent on missing path or lstat errors so startup is never blocked.
func WarnIfKiroSymlink(logsDir string) {
	kiroDir := filepath.Dir(logsDir)
	isLink, err := IsSymlinkPath(kiroDir)
	if err != nil {
		return
	}
	if isLink {
		slog.Warn(".kiro directory is a symlink; proceed with caution", "path", kiroDir)
	}
}
