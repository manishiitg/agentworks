//go:build !windows

package knowledgebase

import "os"

// syncDir flushes a directory entry so a rename or create survives a crash.
func syncDir(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// syncFile flushes a file's contents to disk.
func syncFile(name string) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
