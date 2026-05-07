//go:build !windows

package fileutil

import (
	"os"
	"syscall"
)

// OpenNoFollow opens path read-only and refuses to follow a symlink at path.
// Uses O_NOFOLLOW for an atomic, race-free kernel-level check.
func OpenNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
