package crewsync

import (
	"io/fs"
	"syscall"
)

func stampIdentity(info fs.FileInfo) (ctime int64, device, inode uint64) {
	if s, ok := info.Sys().(*syscall.Stat_t); ok {
		return s.Ctimespec.Nano(), uint64(s.Dev), s.Ino
	}
	return
}
