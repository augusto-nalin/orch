//go:build !windows

package state

import (
	"os"
	"syscall"
)

const (
	lockShared    = syscall.LOCK_SH
	lockExclusive = syscall.LOCK_EX
)

func lockFile(f *os.File, how int) error { return syscall.Flock(int(f.Fd()), how) }

func unlockFile(f *os.File) { syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
