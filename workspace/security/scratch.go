//go:build !windows

package security

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

var scratchSweep sync.Once

// A private service-user root prevents other users planting cleanup targets.
// Locks distinguish live commands from directories left by a crashed service.
func allocateScratch() (string, func(), error) {
	root := fmt.Sprintf("/tmp/aws-%d", os.Getuid())
	if err := os.Mkdir(root, 0700); err != nil && !os.IsExist(err) {
		return "", nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0700 || stat.Uid != uint32(os.Getuid()) {
		return "", nil, fmt.Errorf("unsafe scratch root %s", root)
	}
	scratchSweep.Do(func() {
		entries, _ := os.ReadDir(root)
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(root, entry.Name())
			fd, err := syscall.Open(filepath.Join(path, ".owner"), syscall.O_RDWR|syscall.O_NOFOLLOW, 0)
			if err != nil {
				continue
			}
			if syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB) == nil {
				_ = os.RemoveAll(path)
			}
			_ = syscall.Close(fd)
		}
	})
	path, err := os.MkdirTemp(root, "c-")
	if err != nil {
		return "", nil, err
	}
	owner, err := os.OpenFile(filepath.Join(path, ".owner"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		_ = os.RemoveAll(path)
		return "", nil, err
	}
	if err := syscall.Flock(int(owner.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		owner.Close()
		_ = os.RemoveAll(path)
		return "", nil, err
	}
	return path, func() { _ = os.RemoveAll(path); _ = owner.Close() }, nil
}
