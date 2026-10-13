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
