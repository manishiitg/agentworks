package clisandbox

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf8"
)

// UnexpectedExit is what a person reads when a CLI's server process ends on its own: its exit status and, when the
// caller kept one, the last line it wrote to stderr ("Muse stopped unexpectedly (exit 1): out of memory"). The line
// is cut to 200 characters; a caller whose stderr may hold credentials passes "".
func UnexpectedExit(program string, waitErr error, stderrTail string) error {
	status := "exit 0"
	var exit *exec.ExitError
	switch {
	case errors.As(waitErr, &exit) && exit.ExitCode() >= 0:
		status = fmt.Sprintf("exit %d", exit.ExitCode())
	case exit != nil:
		status = exit.ProcessState.String() // killed by a signal
	case waitErr != nil:
		status = waitErr.Error()
	}
	last := ""
	for _, line := range strings.Split(stderrTail, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			last = line
		}
	}
	if utf8.RuneCountInString(last) > 200 {
		last = string([]rune(last)[:200]) + "…"
	}
	if last != "" {
		return fmt.Errorf("%s stopped unexpectedly (%s): %s", program, status, last)
	}
	return fmt.Errorf("%s stopped unexpectedly (%s)", program, status)
}
