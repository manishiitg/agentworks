//go:build windows

package procshutdown

import (
	"errors"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// SetGroup starts cmd in a new process group; KillGroup ends it and everything
// it started (taskkill walks the tree, as Windows has no group signals).
func SetGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// KillGroup ends pid and everything it started; already gone is not an error.
func KillGroup(pid int) error { return killGroup(pid) }

// Alive reports whether pid is still running (one this account may not inspect counts as running).
func Alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == 259 // STILL_ACTIVE
}
