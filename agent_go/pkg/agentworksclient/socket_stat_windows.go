//go:build windows

package agentworksclient

import (
	"errors"

	"golang.org/x/sys/windows"
)

// socketUnreadable reports the Windows error for a local socket file that cannot be opened
// for inspection ("The file cannot be accessed by the system"), as left by a helper that died.
func socketUnreadable(err error) bool { return errors.Is(err, windows.ERROR_CANT_ACCESS_FILE) }
