//go:build linux

package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Walk open directory descriptors with NOFOLLOW so a replaced symlink cannot
// chmod outside the workspace. Only service-owned directories are changed.
// Ancestors get traversal, never write access. Read-only grants stay untouched.
func makeSlotWriteDirectoryUsable(path, root string) error {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("slot write directory must be below the workspace root")
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if openErr != nil {
			return fmt.Errorf("open slot directory: %w", openErr)
		}
		_ = unix.Close(fd)
		fd = next
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			return err
		}
		if st.Uid != uint32(os.Geteuid()) {
			continue
		}
		mode := st.Mode & 07777
		if i == len(parts)-1 {
			mode = (mode &^ 0007) | 0070 | unix.S_ISGID
		} else {
			mode |= 0010
		}
		if mode != st.Mode&07777 {
			if err := unix.Fchmod(fd, mode); err != nil {
				return fmt.Errorf("prepare slot directory permissions: %w", err)
			}
		}
	}
	return nil
}
