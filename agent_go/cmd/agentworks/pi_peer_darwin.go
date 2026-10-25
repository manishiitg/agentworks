//go:build darwin

package main

import "golang.org/x/sys/unix"

func localSocketPeerPID(fd int) (int, error) {
	return unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
}
