//go:build !windows && !darwin && !linux

package server

import "fmt"

func localChatFreeBytes(path string) (uint64, error) {
	return 0, fmt.Errorf("free storage is unavailable")
}
