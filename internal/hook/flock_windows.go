//go:build windows

package hook

import (
	"errors"
	"os"
)

// Hook handler runs once per session per process — no concurrent writes
// to the same file. Flock is unnecessary on Windows; provide no-op stubs.

func flockExclusive(_ *os.File) error { return nil }
func flockUnlock(_ *os.File)          {}
func flockRotate(_ *os.File) error    { return errors.New("not supported") }
