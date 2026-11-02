//go:build !windows

package server

import "syscall"

// ownProcessGroup starts a helper process in its own process group.
func ownProcessGroup() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
