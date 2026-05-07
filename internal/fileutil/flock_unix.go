//go:build !windows

package fileutil

import (
	"os"
	"syscall"
)

// FlockExclusive acquires an exclusive advisory lock on f.
func FlockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

// FlockUnlock releases the advisory lock on f.
func FlockUnlock(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
