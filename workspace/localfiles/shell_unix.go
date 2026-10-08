//go:build darwin || linux

package localfiles

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureShellProcess(cmd *exec.Cmd) func() {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	kill := func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		kill()
		return nil
	}
	cmd.WaitDelay = time.Second
	return kill
}
