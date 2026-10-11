//go:build !darwin && !linux

package main

import "errors"

func piSocketPeerPID(_ int) (int, error) {
	return 0, errors.New("Pi SDK peer identity is unsupported on this platform")
}
