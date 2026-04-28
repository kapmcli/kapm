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
	conn, err := f.SyscallConn()
	if err != nil {
		return
	}
	_ = conn.Control(func(fd uintptr) {
		var overlapped windows.Overlapped
		_ = windows.UnlockFileEx(windows.Handle(fd), 0, lockLength, 0, &overlapped)
	})
}

func flockRotate(f *os.File) error {
	return lockFile(f, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY)
}

func lockFile(f *os.File, flags uint32) error {
	conn, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var lockErr error
	if err := conn.Control(func(fd uintptr) {
		var overlapped windows.Overlapped
		lockErr = windows.LockFileEx(windows.Handle(fd), flags, 0, lockLength, 0, &overlapped)
	}); err != nil {
		return err
	}
	return lockErr
}
