//go:build !windows

package server

import (
	"os"
	"syscall"
)

func desktopSocketOwnedByUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}
