//go:build !windows

package tmuxcontrol

import "syscall"

func termProcess(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

func killProcess(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }
