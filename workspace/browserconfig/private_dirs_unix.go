//go:build unix

package browserconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Keep directory descriptors open while walking: a pathname check followed by
// MkdirAll would follow a replacement symlink in the world-writable tmp root.
func ensurePrivateDirectories(root string, children ...string) error {
	fd, err := unix.Open(filepath.Dir(root), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open browser temporary parent: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	for _, name := range append([]string{filepath.Base(root)}, children...) {
		if err := unix.Mkdirat(fd, name, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
			return fmt.Errorf("create private browser directory %s: %w", name, err)
		}
		next, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("open private browser directory %s: %w", name, err)
		}
		var st unix.Stat_t
		err = unix.Fstat(next, &st)
		if err == nil && (st.Uid != uint32(os.Geteuid()) || st.Mode&07777 != 0700) {
			err = fmt.Errorf("must belong to the service account with mode 0700")
		}
		if err != nil {
			_ = unix.Close(next)
			return fmt.Errorf("unsafe private browser directory %s: %w", name, err)
		}
		_ = unix.Close(fd)
		fd = next
	}
	return nil
}
