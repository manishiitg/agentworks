package server

import (
	"os"
	"syscall"
)

func crewFolderHasSlotAccess(path string, gid int) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.IsDir() && stat.Gid == uint32(gid) && info.Mode().Perm() == 0770 && info.Mode()&os.ModeSetgid != 0
}
