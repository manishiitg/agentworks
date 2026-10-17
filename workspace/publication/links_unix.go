//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package publication

import (
	"fmt"
	"os"
	"syscall"
)

func publicationMultipleLinks(file *os.File, initial os.FileInfo) (bool, error) {
	if stat, ok := initial.Sys().(*syscall.Stat_t); ok && uint64(stat.Nlink) > 1 {
		return true, nil
	}
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, fmt.Errorf("file link count unavailable")
	}
	return uint64(stat.Nlink) > 1, nil
}
