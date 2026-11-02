//go:build windows

package browserreap

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code Windows reports while a process runs.
const stillActive = 259

// ProcessAlive reports whether pid is a live process. A process this account
// may not inspect is alive: its session's files are not this account's to judge.
func ProcessAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == stillActive
}

// fileOwnerUID reports no owner: the Windows app is one person's, so a session
// file is treated as theirs, and the Linux /proc scan finds nothing to own.
func fileOwnerUID(os.FileInfo) (int, bool) { return 0, false }

// Windows has no graceful stop signal for another process, so stopping ends it.
func stopProcess(pid int) error { return killProcess(pid) }

func killProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
