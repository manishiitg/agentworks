//go:build !linux && !darwin

package main

import "errors"

func cursorProcessAt(pid int) (cursorProcess, error) {
	return cursorProcess{PID: pid}, errors.New("Cursor process verification is unavailable on this host")
}

func cursorPIDDefinitelyGone(int) bool { return false }
