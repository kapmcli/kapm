package convert

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/kapmcli/kapm/internal/fileutil"
)

func copyFile(src, dst string, mode os.FileMode) (err error) {
	in, err := fileutil.OpenNoFollow(src)
	if err != nil {
		return fmt.Errorf("convert open %q: %w", src, err)
	}
	defer func() {
		if closeErr := in.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("convert close %q: %w", src, closeErr)
		}
	}()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("convert mkdir %q: %w", filepath.Dir(dst), err)
	}
	return fileutil.WriteTempFileFrom(dst, in, mode.Perm()&0o755)
}

// CopyDirectoryContents recursively copies src into dst while skipping symlinks
// with warnings. It preserves file modes up to 0o755.
func CopyDirectoryContents(src, dst string) error {
	return copyDirectoryContents(src, dst)
}

func copyDirectoryContents(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("convert read dir %q: %w", src, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("convert mkdir %q: %w", dst, err)
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if fileutil.IsSymlinkMode(entry.Type()) {
			slog.Warn("kapm skip symlink", "path", srcPath)
			continue
		}

		if entry.IsDir() {
			if err := copyDirectoryContents(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("convert stat %q: %w", srcPath, err)
		}

		if err := copyFile(srcPath, dstPath, info.Mode()); err != nil {
			return err
		}
	}

	return nil
}

func copyDirectory(src, dst string, force bool) (bool, error) {
	if force {
		if _, err := os.Lstat(src); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return false, nil
			}
			return false, fmt.Errorf("convert stat %q: %w", src, err)
		}
		if isLink, err := fileutil.IsSymlinkPath(dst); err == nil && isLink {
			return false, fmt.Errorf("convert remove %q: refuse to remove symlink", dst)
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("convert stat %q: %w", dst, err)
		}
		if err := os.RemoveAll(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("convert remove %q: %w", dst, err)
		}
	} else if _, err := os.Stat(dst); err == nil {
		slog.Warn("kapm skip existing output", "path", dst)
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("convert stat %q: %w", dst, err)
	}

	if err := copyDirectoryContents(src, dst); err != nil {
		return false, err
	}

	return true, nil
}
