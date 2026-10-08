//go:build !darwin && !linux

package localfiles

import "os/exec"

func configureShellProcess(cmd *exec.Cmd) func() {
	return func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
}
