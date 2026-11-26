//go:build windows

package tmuxcontrol

import "os"

// Windows has no tmux and no polite stop signal; both end the process.
func termProcess(pid int) error { return killProcess(pid) }

func killProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
