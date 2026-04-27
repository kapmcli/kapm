package hook

import (
	"errors"
	"syscall"
)

// isShareViolation reports whether err is a Windows ERROR_SHARING_VIOLATION,
// which occurs when another process holds the file open during rename.
func isShareViolation(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		// ERROR_SHARING_VIOLATION = 32
		return errno == 32
	}
	return false
}
