//go:build !windows

package server

import "os"

// syncDirectory flushes a directory entry so a rename or create in it survives a crash.
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
