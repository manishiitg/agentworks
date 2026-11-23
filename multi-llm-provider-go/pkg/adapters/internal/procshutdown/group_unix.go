//go:build !windows

package procshutdown

import (
	"os/exec"
	"syscall"
)

// SetGroup starts cmd as the leader of its own process group, so KillGroup and
// Graceful can end every process the CLI starts (launchers start the real binary).
func SetGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// KillGroup ends the process group led by pid; already gone is not an error.
func KillGroup(pid int) error { return killGroup(pid) }

// Alive reports whether pid is still running (a process of another account counts as running).
func Alive(pid int) bool { return syscall.Kill(pid, 0) != syscall.ESRCH }
