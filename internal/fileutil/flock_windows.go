//go:build windows

package fileutil

import "os"

// FlockExclusive is a no-op on Windows; see flock_unix.go for the Unix implementation.
func FlockExclusive(_ *os.File) error { return nil }

// FlockUnlock is a no-op on Windows.
func FlockUnlock(_ *os.File) {}
