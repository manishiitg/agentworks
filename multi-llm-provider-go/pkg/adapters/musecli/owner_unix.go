//go:build !windows

package musecli

import (
	"os"
	"syscall"
)

// ownedByCurrentUser reports whether info belongs to this process's user.
func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid()
}
