package fileutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// OpenSafeLogFile opens (or creates) a log file at logDir/filename under root,
// guarding against symlink-based path traversal attacks.
//
// The directory check is performed twice — before and after os.MkdirAll — to
// mitigate a TOCTOU race: an attacker could replace a legitimate directory with
// a symlink between the first check and the mkdir call. The post-mkdir check
// catches that substitution.
//
// Returns the open file with an exclusive flock held. The caller is responsible
// for defer f.Close() and defer FlockUnlock(f).
func OpenSafeLogFile(root, logDir, filename string) (*os.File, error) {
	// Pre-mkdir symlink check.
	if err := RefuseSymlinkPathUnder(root, logDir); err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir %q: %w", logDir, err)
	}
	// Post-mkdir symlink check: mitigates TOCTOU race where an attacker could
	// replace the directory with a symlink between the first check and mkdir.
	if err := RefuseSymlinkPathUnder(root, logDir); err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	logPath := filepath.Join(logDir, filename)
	if err := RefuseSymlinkPathUnder(root, logPath); err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", logPath, err)
	}
	// FlockExclusive is a no-op on Windows; see fileutil/flock_windows.go.
	if err := FlockExclusive(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("flock %q: %w", logPath, err)
	}
	return f, nil
}
