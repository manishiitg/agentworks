//go:build !windows

// Package filelock takes non-blocking exclusive file locks that the OS releases
// when the process dies, on Unix (flock) and Windows (LockFileEx).
package filelock

import (
	"errors"
	"os"
	"syscall"
)

// TryLock takes an exclusive lock on f without waiting. ok is false, with no
// error, when another process already holds it.
func TryLock(f *os.File) (ok bool, err error) {
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	return false, err
}

// Unlock releases a lock taken with TryLock.
func Unlock(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
