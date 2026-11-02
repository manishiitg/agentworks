//go:build !windows

package browserreap

import (
	"errors"
	"os"
	"syscall"
)

// ProcessAlive reports whether pid is a live process. A process of another
// account (EPERM) is alive: its session's files are not this account's to judge.
func ProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// fileOwnerUID is the account that owns info's file, when the system reports one.
func fileOwnerUID(info os.FileInfo) (int, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}

func stopProcess(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

func killProcess(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }
