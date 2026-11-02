//go:build windows

package server

// signalWindowChange: Windows has no tmux panes or SIGWINCH; the Windows
// terminal (ConPTY, PLAT-874-5) repaints through its own resize call.
func signalWindowChange(int) error { return nil }
