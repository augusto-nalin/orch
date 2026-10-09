package state

import (
	"os"
	"syscall"
	"unsafe"
)

// LockFileEx/UnlockFileEx from kernel32, called directly (syscall doesn't wrap them).
const (
	lockShared    = 0
	lockExclusive = 2 // LOCKFILE_EXCLUSIVE_LOCK
)

var (
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	lockFileEx   = kernel32.NewProc("LockFileEx")
	unlockFileEx = kernel32.NewProc("UnlockFileEx")
)

func lockFile(f *os.File, how int) error {
	var ol syscall.Overlapped
	r, _, err := lockFileEx.Call(f.Fd(), uintptr(how), 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
	if r == 0 {
		return err
	}
	return nil
}

func unlockFile(f *os.File) {
	var ol syscall.Overlapped
	unlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
}
