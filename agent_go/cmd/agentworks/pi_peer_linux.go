//go:build linux

package main

import "golang.org/x/sys/unix"

func piSocketPeerPID(fd int) (int, error) {
	credentials, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, err
	}
	return int(credentials.Pid), nil
}
