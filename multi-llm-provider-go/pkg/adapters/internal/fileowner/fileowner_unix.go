//go:build !windows

// Package fileowner answers "who owns this file" for the adapters' safety checks.
package fileowner

import (
	"os"
	"syscall"
)

// IDs are the Unix account and group that own info's file.
func IDs(info os.FileInfo) (uid, gid int, ok bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(st.Uid), int(st.Gid), true
}

// OwnedByMe reports whether this process's account owns the file at path (info is its Lstat/Stat).
func OwnedByMe(_ string, info os.FileInfo) bool {
	uid, _, ok := IDs(info)
	return ok && uid == os.Getuid()
}
