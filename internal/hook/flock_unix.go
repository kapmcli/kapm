// NOTE: CI-sensitive. The hook-handler file-lock implementation has
// historically been fragile across platforms; this current form settled
// after multiple CI failures. Do not modify without running the full
// Windows + Linux test matrix.
//
// Kiro assigns distinct session IDs to each sub-agent invocation, so
// different processes always write to different <session>.jsonl files.
// The Unix flock guards against the rare case of the same session being
// re-entered within the same process.

//go:build !windows

package hook

import (
	"os"
	"syscall"
)

func flockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func flockUnlock(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// flockRotate acquires an exclusive non-blocking lock on f.
// Returns an error if the lock cannot be acquired immediately.
func flockRotate(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
