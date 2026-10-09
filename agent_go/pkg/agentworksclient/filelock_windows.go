//go:build windows

package agentworksclient

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFileExclusive blocks until it holds an exclusive lock on f; the returned function releases it.
func lockFileExclusive(f *os.File) (func(), error) {
	handle := windows.Handle(f.Fd())
	overlapped := new(windows.Overlapped)
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped); err != nil {
		return nil, err
	}
	return func() { _ = windows.UnlockFileEx(handle, 0, 1, 0, new(windows.Overlapped)) }, nil
}
