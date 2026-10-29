package agentworksclient

import (
	"errors"
	"fmt"
	"os"
	"syscall"

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
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, fmt.Errorf("this local bridge or native session already has a listener: %w", err)
		}
		var code syscall.Errno
		if errors.As(err, &code) {
			return nil, fmt.Errorf("cannot lock AgentWorks bridge (win32=%d): %w", code, err)
		}
		return nil, fmt.Errorf("cannot lock AgentWorks bridge: %w", err)
	}
	return func() { _ = windows.UnlockFileEx(handle, 0, 1, 0, new(windows.Overlapped)); _ = f.Close() }, nil
}
