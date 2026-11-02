//go:build !windows

package server

import (
	"os"
	"syscall"
)

func openProjectOwnerLock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
}
