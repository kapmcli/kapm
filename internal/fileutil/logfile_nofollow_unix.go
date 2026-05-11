//go:build !windows

package fileutil

import "syscall"

const openNoFollow = syscall.O_NOFOLLOW
