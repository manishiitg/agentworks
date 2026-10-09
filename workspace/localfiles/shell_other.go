//go:build !darwin && !linux && !windows

package localfiles

import (
	"context"
	"errors"
	"os/exec"
)

func configureShellProcess(cmd *exec.Cmd) func() {
	return func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
}

func runShellCommand(cmd *exec.Cmd) error { return cmd.Run() }

func windowsShellCommand(context.Context, string, string) (*exec.Cmd, func(), error) {
	return nil, nil, errors.New("not Windows")
}
