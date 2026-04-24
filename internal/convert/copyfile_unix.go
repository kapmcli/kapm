//go:build !windows

package convert

import (
	"os"
	"syscall"
)

// openNoFollow opens src for reading and refuses to follow a symlink at src.
// Uses O_NOFOLLOW for an atomic, race-free check in the kernel.
func openNoFollow(src string) (*os.File, error) {
	return os.OpenFile(src, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
