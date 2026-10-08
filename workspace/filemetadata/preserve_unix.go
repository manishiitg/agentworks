//go:build linux || darwin

package filemetadata

import (
	"os"
	"syscall"
)

// Preserve keeps an atomic replacement editable by the original owner.
// Fail before rename if the service cannot retain the existing authority.
func Preserve(dst, src *os.File) error {
	info, err := src.Stat()
	if err != nil {
		return err
	}
	owner := info.Sys().(*syscall.Stat_t)
	current, err := dst.Stat()
	if err != nil {
		return err
	}
	tempOwner := current.Sys().(*syscall.Stat_t)
	if owner.Uid != tempOwner.Uid || owner.Gid != tempOwner.Gid {
		if err := dst.Chown(int(owner.Uid), int(owner.Gid)); err != nil {
			return err
		}
	}
	if err := dst.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	return preserveACL(dst, src)
}
