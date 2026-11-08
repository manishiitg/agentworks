package workflowfiles

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ServerFileModes keeps state service-writable and slot-readable. Only a
// setgid directory opts into its existing slot group; local state stays private.
func ServerFileModes(root *os.Root) (fs.FileMode, fs.FileMode, error) {
	info, err := root.Stat(".")
	if err != nil {
		return 0, 0, err
	}
	if info.Mode()&fs.ModeSetgid != 0 {
		return fs.ModeSetgid | 0750, 0640, nil
	}
	return 0700, 0600, nil
}

// ProtectServerTree repairs old helper trees before launching a slot. Anchored
// traversal refuses links rather than changing permissions on their targets.
func ProtectServerTree(root *os.Root) error {
	dirMode, fileMode, err := ServerFileModes(root)
	if err != nil {
		return err
	}
	return fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fileError(403, "symbolic links are blocked in helper state")
		}
		mode := fileMode
		if entry.IsDir() {
			mode = dirMode
		} else if !entry.Type().IsRegular() {
			return fileError(403, "helper state must contain regular files")
		}
		return root.Chmod(path, mode)
	})
}

// WriteServerFile is for already-authorized helper state only. Its caller
// supplies an anchored helper directory, never an authored filesystem root.
func WriteServerFile(root *os.Root, relative string, data []byte) error {
	clean, err := CleanRelative(relative)
	if err != nil || clean == "." {
		return fileError(400, "invalid helper filename")
	}
	dirMode, fileMode, err := ServerFileModes(root)
	if err != nil {
		return err
	}
	current, err := root.OpenRoot(".")
	if err != nil {
		return err
	}
	defer func() { current.Close() }()
	if err := current.Chmod(".", dirMode); err != nil {
		return err
	}
	for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(clean)), "/") {
		if part == "." {
			continue
		}
		next, err := OpenDirectory(current, part, true)
		if err != nil {
			return err
		}
		current.Close()
		current = next
		if err := current.Chmod(".", dirMode); err != nil {
			return err
		}
	}
	name := filepath.Base(clean)
	if info, err := current.Lstat(name); err == nil && (!info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0) {
		return fileError(403, "helper state must be a regular file")
	}
	if err := atomicRootWrite(current, name, data, fileMode); err != nil {
		return err
	}
	// The common writer preserves old metadata; helper state instead requires
	// its server-owned mode even when replacing an older group-writable file.
	return current.Chmod(name, fileMode)
}
