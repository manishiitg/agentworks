//go:build windows

package chatlog

import (
	"os"

	"golang.org/x/sys/windows"
)

const isWindows = true

func lockFile(file *os.File) error {
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, new(windows.Overlapped))
}

func unlockFile(file *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, new(windows.Overlapped))
}

// Windows has no directory fsync; the rename itself is durable enough here.
func syncDir(string) {}
