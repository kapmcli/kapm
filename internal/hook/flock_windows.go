//go:build windows

package hook

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// Windows LockFileEx with Go's overlapped-I/O file handles returns
// "Access is denied". We use the Win32 LockFileEx API directly via
// syscall, obtaining the raw handle through SyscallConn.Read so the
// runtime keeps the handle valid for the duration of the call.

var (
	modkernel32      = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = modkernel32.NewProc("LockFileEx")
	procUnlockFileEx = modkernel32.NewProc("UnlockFileEx")
)

const (
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
)

type overlapped struct {
	Internal     uintptr
	InternalHigh uintptr
	Offset       uint32
	OffsetHigh   uint32
	HEvent       syscall.Handle
}

func lockFileEx(h syscall.Handle, flags uint32) error {
	var ol overlapped
	r1, _, err := procLockFileEx.Call(
		uintptr(h),
		uintptr(flags),
		0,
		1, 0,
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 == 0 {
		return fmt.Errorf("%w", err)
	}
	return nil
}

func unlockFileEx(h syscall.Handle) {
	var ol overlapped
	procUnlockFileEx.Call(
		uintptr(h),
		0,
		1, 0,
		uintptr(unsafe.Pointer(&ol)),
	)
}

func flockExclusive(f *os.File) error {
	var sysErr error
	conn, err := f.SyscallConn()
	if err != nil {
		return err
	}
	err = conn.Read(func(fd uintptr) bool {
		sysErr = lockFileEx(syscall.Handle(fd), lockfileExclusiveLock)
		return true
	})
	if err != nil {
		return err
	}
	return sysErr
}

func flockUnlock(f *os.File) {
	conn, err := f.SyscallConn()
	if err != nil {
		return
	}
	_ = conn.Read(func(fd uintptr) bool {
		unlockFileEx(syscall.Handle(fd))
		return true
	})
}

func flockRotate(f *os.File) error {
	var sysErr error
	conn, err := f.SyscallConn()
	if err != nil {
		return err
	}
	err = conn.Read(func(fd uintptr) bool {
		sysErr = lockFileEx(syscall.Handle(fd), lockfileExclusiveLock|lockfileFailImmediately)
		return true
	})
	if err != nil {
		return err
	}
	return sysErr
}
