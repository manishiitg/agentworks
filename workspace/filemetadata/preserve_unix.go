//go:build linux || darwin

package filemetadata

import (
	"errors"
	"os"
	"syscall"
)

// Preserve keeps an atomic replacement accessible to the original owner.
// Rootless Linux services retain the group and use an ACL for the former owner
// when they cannot assign its UID. Fail before rename if access cannot be kept.
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
			if errors.Is(err, syscall.EPERM) {
				return preserveUnownedFile(dst, src, info, owner.Uid, owner.Gid)
			}
			return err
		}
	}
	if err := dst.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	return preserveACL(dst, src)
}
