package hook

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
		// dir may not exist yet — not an error
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
		if err := compressFile(src, gz); err != nil {
			_, _ = fmt.Fprintf(stderr, "hook-handler: rotate %q: %v\n", src, err)
		}
	}
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
		if errors.Is(err, fs.ErrNotExist) {
			return nil // already rotated by another process
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
	// read-only close: ignoring Close error is safe; no data integrity concern
	defer func() { _ = f.Close() }()

	if err := flockRotate(f); err != nil {
		// Another process holds the lock — leave rotating in place; it will finish.
		return nil
	}
	defer flockUnlock(f)

	// Re-check: another process may have already created the .gz.
	if _, err := os.Stat(dst); err == nil {
		// best-effort; file may have been removed by another process
		_ = os.Remove(rotating)
		return nil
	}

	tmp := dst + ".tmp"
	if err := writeGzip(f, tmp); err != nil {
		_ = os.Remove(tmp) // best-effort; tmp may not exist
		// Restore source on failure so data isn't lost.
		if rerr := os.Rename(rotating, src); rerr != nil {
			return errors.Join(fmt.Errorf("compress %s: %w", src, err), fmt.Errorf("restore: %w", rerr))
		}
		return fmt.Errorf("compress %s: %w", src, err)
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
