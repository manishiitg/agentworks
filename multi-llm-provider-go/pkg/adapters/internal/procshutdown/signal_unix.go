//go:build !windows

package procshutdown

import (
	"errors"
	"syscall"
)

// errProcessGone means the process group had already exited.
var errProcessGone = errors.New("process already gone")

// termGroup asks the process group led by pid to stop (SIGTERM).
func termGroup(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			return errProcessGone
		}
		return err
	}
	return nil
}

// killGroup ends the process group led by pid (SIGKILL); already gone is not an error.
func killGroup(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}
