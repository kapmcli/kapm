//go:build windows

package hook

import (
	"errors"
	"os"
	"sync"
	"syscall"
)

// Windows: Go opens files with FILE_FLAG_OVERLAPPED, making LockFileEx
// return "Access is denied" regardless of how the handle is obtained
// (Fd, SyscallConn.Control, SyscallConn.Read). Use a process-local
// mutex keyed by path for in-process serialisation, which is sufficient
// for the hook handler (one write per invocation).

var (
	mu    sync.Mutex
	locks = map[string]*sync.Mutex{}
)

func pathMu(f *os.File) *sync.Mutex {
	mu.Lock()
	defer mu.Unlock()
	p := f.Name()
	m, ok := locks[p]
	if !ok {
		m = &sync.Mutex{}
		locks[p] = m
	}
	return m
}

func flockExclusive(f *os.File) error {
	pathMu(f).Lock()
	return nil
}

func flockUnlock(f *os.File) {
	pathMu(f).Unlock()
}

func flockRotate(f *os.File) error {
	m := pathMu(f)
	if !m.TryLock() {
		return errors.New("lock held")
	}
	return nil
}
