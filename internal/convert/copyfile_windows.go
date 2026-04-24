//go:build windows

// Windows does not expose O_NOFOLLOW. openNoFollow uses Lstat-before-open,
// which leaves a narrow TOCTOU window between the Lstat and os.Open calls.
// This is a best-effort defense; Unix gets hard protection via O_NOFOLLOW.

package convert

import (
	"fmt"
	"os"

	"github.com/kapmcli/kapm/internal/fileutil"
)

// openNoFollow opens src for reading and refuses to follow a symlink at src.
// Lstats src first and returns an error if src is a symlink, then opens.
func openNoFollow(src string) (*os.File, error) {
	isLink, err := fileutil.IsSymlinkPath(src)
	if err != nil {
		return nil, err
	}
	if isLink {
		return nil, fmt.Errorf("convert open %q: refuse to follow symlink", src)
	}
	return os.Open(src)
}
