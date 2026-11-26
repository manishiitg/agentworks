//go:build !windows

package server

import (
	"os"
	"syscall"
)

func openCrewOfflineAppAppend(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
}
