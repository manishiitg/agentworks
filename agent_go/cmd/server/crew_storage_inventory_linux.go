//go:build linux

package server

import (
	"encoding/base64"
	"errors"
	"io/fs"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// Capture ownership and every readable xattr, including POSIX ACLs. This is a
// private backup manifest, never an MCP or website file-content response.
func crewStorageMetadata(name string, info fs.FileInfo) (map[string]any, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, errors.New("file ownership is unavailable")
	}
	out := map[string]any{"uid": st.Uid, "gid": st.Gid, "mode": st.Mode, "links": st.Nlink, "device": st.Dev}
	size, err := unix.Llistxattr(name, nil)
	if errors.Is(err, unix.ENOTSUP) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	attrs := map[string]string{}
	if size > 0 {
		names := make([]byte, size)
		size, err = unix.Llistxattr(name, names)
		if err != nil {
			return nil, err
		}
		for _, key := range strings.Split(strings.TrimRight(string(names[:size]), "\x00"), "\x00") {
			n, err := unix.Lgetxattr(name, key, nil)
			if err != nil {
				return nil, err
			}
			value := make([]byte, n)
			n, err = unix.Lgetxattr(name, key, value)
			if err != nil {
				return nil, err
			}
			attrs[key] = base64.StdEncoding.EncodeToString(value[:n])
		}
	}
	out["xattrs"] = attrs
	return out, nil
}

func crewStorageFreeBytes(dir string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
