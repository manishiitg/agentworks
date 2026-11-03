//go:build !windows

package agentworksclient

import (
	"errors"
	"os"
)

// CheckPrivateAccess checks access after the caller has checked the file type.
func CheckPrivateAccess(_ string, info os.FileInfo) error {
	if info.Mode().Perm()&0077 != 0 {
		return errors.New("file or directory is accessible to other operating-system users")
	}
	return nil
}

// SecurePrivateFile restricts a newly created temporary file before writing.
func SecurePrivateFile(f *os.File) error { return f.Chmod(0600) }

func privateConfigAccessHint() string { return "; run chmod 600 on the AgentWorks config file" }

// slotGroupDir reports the slot layout (2770: setgid, the slot group's, closed to others), the same rule the CLI
// runtime uses for a slot's state folders (PLAT-872-84).
func slotGroupDir(info os.FileInfo) bool {
	return info.Mode()&os.ModeSetgid != 0 && info.Mode().Perm()&0o070 == 0o070 && info.Mode().Perm()&0o007 == 0
}
