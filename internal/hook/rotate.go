package hook

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// testHookAfterClaim is called in tests after the initial rename succeeds, before open.
var testHookAfterClaim func()

// rotate compresses .jsonl files in logDir that are older than minAge.
// The current session and files that already have a .jsonl.gz counterpart are skipped.
// Errors are logged to stderr and skipped; the function always returns nil.
func rotate(logDir, currentSessionID string, stderr io.Writer, minAge time.Duration) {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return
		}
		slog.Warn("hook rotate: read logs dir failed", "dir", logDir, "err", err)
		return
	}
	now := time.Now()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if e.Name() == currentSessionID+".jsonl" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) < minAge {
			continue
		}
		src := filepath.Join(logDir, e.Name())
		gz := src + ".gz"
		if _, err := os.Stat(gz); err == nil {
			// already compressed
			continue
		}
		if err := compressWithRetry(src, gz); err != nil {
			slog.Warn("hook-handler: rotate compress failed", "src", src, "err", fmt.Errorf("rotate compress %q: %w", src, err))
		}
	}
}

func compressWithRetry(src, dst string) error {
	err := compressFile(src, dst)
	if err == nil {
		return nil
	}
	if !shouldRetryCompress(src, dst) {
		return err
	}
	if retryErr := compressFile(src, dst); retryErr != nil {
		return errors.Join(err, fmt.Errorf("retry: %w", retryErr))
	}
	return nil
}

func shouldRetryCompress(src, dst string) bool {
	if _, err := os.Stat(dst); err == nil {
		return false
	}
	if _, err := os.Stat(src); err == nil {
		return true
	}
	return false
}

// compressFile gzip-compresses src to dst atomically via a .tmp file.
// It renames src to src.rotating first, atomically claiming it so concurrent
// callers get ErrNotExist and return nil.
func compressFile(src, dst string) error {
	// Rename source to claim it atomically. If another process is writing
	// to src, the rename succeeds and the writer's file descriptor still
	// points to the renamed inode (Unix semantics).
	rotating := src + ".rotating"
	if err := os.Rename(src, rotating); err != nil {
		if errors.Is(err, fs.ErrNotExist) || isShareViolation(err) {
			return nil // already rotated or being rotated by another process
		}
		return err
	}
	if testHookAfterClaim != nil {
		testHookAfterClaim()
	}

	f, err := os.Open(rotating)
	if err != nil {
		// Rename succeeded but open failed — try to restore.
		if rerr := os.Rename(rotating, src); rerr != nil {
			return errors.Join(err, fmt.Errorf("restore: %w", rerr))
		}
		return err
	}

	locked := false
	closeRotating := func() error {
		if locked {
			flockUnlock(f)
			locked = false
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("close rotating file: %w", err)
		}
		return nil
	}

	if err := flockRotate(f); err != nil {
		// Another process holds the lock — leave rotating in place; it will finish.
		_ = f.Close()
		return nil
	}
	locked = true

	// Re-check: another process may have already created the .gz.
	if _, err := os.Stat(dst); err == nil {
		closeErr := closeRotating()
		// best-effort; file may have been removed by another process
		_ = os.Remove(rotating)
		return closeErr
	}

	tmp := dst + ".tmp"
	if err := writeGzip(f, tmp); err != nil {
		closeErr := closeRotating()
		_ = os.Remove(tmp) // best-effort; tmp may not exist
		// Restore source on failure so data isn't lost.
		if rerr := os.Rename(rotating, src); rerr != nil {
			return errors.Join(fmt.Errorf("compress %s: %w", src, err), closeErr, fmt.Errorf("restore: %w", rerr))
		}
		return errors.Join(fmt.Errorf("compress %s: %w", src, err), closeErr)
	}
	if err := closeRotating(); err != nil {
		_ = os.Remove(tmp) // best-effort; tmp may not exist
		if rerr := os.Rename(rotating, src); rerr != nil {
			return errors.Join(err, fmt.Errorf("restore: %w", rerr))
		}
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp) // best-effort; tmp may not exist
		if rerr := os.Rename(rotating, src); rerr != nil {
			return errors.Join(err, fmt.Errorf("restore: %w", rerr))
		}
		return err
	}
	// best-effort; file may have been removed by another process
	_ = os.Remove(rotating)
	return nil
}

func writeGzip(r io.Reader, dst string) error {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	return writeGzipTo(r, out)
}

// writeGzipTo gzip-compresses r into out, joining all close errors via errors.Join.
func writeGzipTo(r io.Reader, out io.WriteCloser) error {
	gw, err := gzip.NewWriterLevel(out, gzip.BestSpeed)
	if err != nil {
		return errors.Join(fmt.Errorf("gzip writer: %w", err), out.Close())
	}
	if _, err := io.Copy(gw, r); err != nil {
		return errors.Join(err, gw.Close(), out.Close())
	}
	if err := gw.Close(); err != nil {
		return errors.Join(err, out.Close())
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close gzip output: %w", err)
	}
	return nil
}
