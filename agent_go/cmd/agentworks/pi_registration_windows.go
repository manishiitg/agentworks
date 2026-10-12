//go:build windows

package main

import (
	"errors"
	"os"
)

func openPiRegistration(string) (*os.File, error) {
	return nil, errors.New("Pi Unix activation is unavailable on Windows")
}
func piPrivateInfo(os.FileInfo) bool { return false }

func piProcessGone(int) bool { return false }
