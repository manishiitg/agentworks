package agentworksclient

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// liveFileLock takes the bridge's single-listener lock: an exclusive lock on a private file that
// the system drops if the process dies. A second listener is refused at once.
func liveFileLock(path string) (func(), error) {
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || CheckPrivateAccess(path, info) != nil) {
		return nil, errors.New("unsafe live lock")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	handle := windows.Handle(f.Fd())
	if err = windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped)); err != nil {
		_ = f.Close()
		return nil, errors.New("this local bridge or native session already has a listener")
	}
	return func() { _ = windows.UnlockFileEx(handle, 0, 1, 0, new(windows.Overlapped)); _ = f.Close() }, nil
}
