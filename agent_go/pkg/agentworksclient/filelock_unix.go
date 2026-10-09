//go:build !windows

package agentworksclient

import (
	"os"
	"syscall"
)

// lockFileExclusive blocks until it holds an exclusive lock on f; the returned function releases it.
func lockFileExclusive(f *os.File) (func(), error) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }, nil
}
