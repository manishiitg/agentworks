//go:build darwin

package main

import "golang.org/x/sys/unix"

func piSocketPeerPID(fd int) (int, error) {
	return unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
}
