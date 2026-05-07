//go:build windows

// Windows does not expose O_NOFOLLOW. OpenNoFollow uses Lstat-before-open,
// which leaves a narrow TOCTOU window between the Lstat and os.Open calls.
// This is a best-effort defense; Unix gets hard protection via O_NOFOLLOW.
package fileutil

import (
	"fmt"
	"os"
)

// OpenNoFollow opens path read-only and refuses to follow a symlink at path.
// See the file-level comment for the Windows TOCTOU caveat.
func OpenNoFollow(path string) (*os.File, error) {
	isLink, err := IsSymlinkPath(path)
	if err != nil {
		return nil, err
	}
	if isLink {
		return nil, fmt.Errorf("refuse to follow symlink: %s", path)
	}
	return os.Open(path)
}
