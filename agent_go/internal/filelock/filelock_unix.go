//go:build !windows

// Package filelock takes non-blocking exclusive file locks that the OS releases
// when the process dies, on Unix (flock) and Windows (LockFileEx).
package filelock

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// TryLock takes an exclusive lock on f without waiting. ok is false, with no
// error, when another process already holds it.
func TryLock(f *os.File) (ok bool, err error) {
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return false, err
}

// Unlock releases a lock taken with TryLock.
func Unlock(f *os.File) { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }
