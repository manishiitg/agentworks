//go:build !windows

package crewroot

import (
	"os"
	"syscall"
)

func fileGroup(info os.FileInfo) int {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Gid)
	}
	return -1
}
