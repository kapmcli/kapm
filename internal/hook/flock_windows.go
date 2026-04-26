//go:build windows

package hook

import (
	"os"

	"golang.org/x/sys/windows"
)

const lockLength = 1

func flockExclusive(f *os.File) error {
	return lockFile(f, windows.LOCKFILE_EXCLUSIVE_LOCK)
}

func flockUnlock(f *os.File) {
	var overlapped windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, lockLength, 0, &overlapped)
}

func flockRotate(f *os.File) error {
	return lockFile(f, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY)
}

func lockFile(f *os.File, flags uint32) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, lockLength, 0, &overlapped)
}
