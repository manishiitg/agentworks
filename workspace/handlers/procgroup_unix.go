//go:build !windows

package handlers

import (
	"os/exec"
	"syscall"
)

// Process groups: on Unix a command and everything it starts share one group that is signalled together.

func getProcessGroup(pid int) (int, error) { return syscall.Getpgid(pid) }

func signalProcessGroup(pgid int, sig syscall.Signal) error { return syscall.Kill(-pgid, sig) }

func signalProcess(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }

func processRunning(pid int) bool { return syscall.Kill(pid, 0) == nil }

func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}
