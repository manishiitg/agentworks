//go:build !windows

package browser

import "syscall"

// killChromeTree kills Chrome's whole process group: Chrome's PGID equals its
// own PID, so -PGID takes GPU, renderer and network helpers in one shot.
func killChromeTree(chromePID int) error { return syscall.Kill(-chromePID, syscall.SIGKILL) }
