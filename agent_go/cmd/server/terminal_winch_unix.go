//go:build !windows

package server

import (
	"os"
	"syscall"
)

// signalWindowChange asks a tmux pane's process to repaint at its new size.
func signalWindowChange(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(syscall.SIGWINCH)
}
