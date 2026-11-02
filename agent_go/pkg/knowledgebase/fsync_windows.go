//go:build windows

package knowledgebase

import (
	"errors"
	"io/fs"
	"os"
)

// syncDir: Windows cannot open a directory to flush it ("Access is denied"),
// and NTFS journals directory entries itself, so there is nothing to do.
func syncDir(string) error { return nil }

// syncFile flushes a file's contents. Windows flushes only through a handle
// opened for writing; Git marks its object files read-only, and those are
// written once and never changed, so a read-only file is skipped.
func syncFile(name string) error {
	f, err := os.OpenFile(name, os.O_RDWR, 0)
	if errors.Is(err, fs.ErrPermission) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
