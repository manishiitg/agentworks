//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
)

func openPiRegistration(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func piPrivateInfo(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid()) && info.Mode().Perm()&0077 == 0
}

func piProcessGone(pid int) bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }
