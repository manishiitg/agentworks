//go:build !windows

package cliupdate

import (
	"os/exec"
	"syscall"
)

// killTreeOnCancel runs cmd in its own process group and kills the whole group
// when its context ends, so an installer's children don't outlive it.
func killTreeOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
