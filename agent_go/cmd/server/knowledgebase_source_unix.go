//go:build !windows

package server

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// knowledgeReadSource reads one migration source file without following any
// symlink on the way from the filesystem root.
func knowledgeReadSource(root, relative string) ([]byte, error) {
	if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, "../") {
		return nil, fmt.Errorf("invalid source-relative path")
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("source root must be canonical and absolute")
	}
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() { unix.Close(fd) }()
	parts := strings.Split(strings.TrimPrefix(filepath.ToSlash(filepath.Join(root, relative)), "/"), "/")
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, fmt.Errorf("invalid source-relative path")
		}
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, err := unix.Openat(fd, part, flags, 0)
		if err != nil {
			return nil, err
		}
		unix.Close(fd)
		fd = next
	}
	f := os.NewFile(uintptr(fd), relative)
	defer f.Close()
	fd = -1
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("source must be a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, 10*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 10*1024*1024 {
		return nil, fmt.Errorf("source file exceeds 10 MiB")
	}
	return b, nil
}
