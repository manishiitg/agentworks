//go:build windows

package procshutdown

import (
	"errors"
	"os/exec"
	"strconv"
)

// errProcessGone means the process tree had already exited.
var errProcessGone = errors.New("process already gone")

// termGroup: Windows has no polite stop signal for another process or process
// groups, so it ends pid and everything it started (taskkill walks the tree).
func termGroup(pid int) error {
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 128 { // no such process
			return errProcessGone
		}
		return err
	}
	return nil
}

// killGroup ends pid and its children; already gone is not an error.
func killGroup(pid int) error {
	if err := termGroup(pid); err != nil && !errors.Is(err, errProcessGone) {
		return err
	}
	return nil
}
