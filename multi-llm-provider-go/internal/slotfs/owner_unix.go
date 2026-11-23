//go:build !windows

package slotfs

import (
	"os"
	"os/exec"
	"syscall"
)

// fileUID and fileGID are the account and group that own info's file.
func fileUID(info os.FileInfo) (int, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}

func fileGID(info os.FileInfo) (int, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Gid), true
}

func terminateSlotCommand(cmd *exec.Cmd) error { return cmd.Process.Signal(syscall.SIGTERM) }
