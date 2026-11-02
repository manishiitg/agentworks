//go:build windows

package cliupdate

import (
	"os/exec"
	"strconv"
)

// killTreeOnCancel kills cmd and every process it started when its context
// ends; Windows has no process groups to signal, so taskkill walks the tree.
func killTreeOnCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
