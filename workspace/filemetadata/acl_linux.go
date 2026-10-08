package filemetadata

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func preserveACL(dst, src *os.File) error {
	const name = "system.posix_acl_access"
	n, err := unix.Fgetxattr(int(src.Fd()), name, nil)
	if errors.Is(err, unix.ENODATA) || errors.Is(err, unix.ENOTSUP) {
		err = unix.Fremovexattr(int(dst.Fd()), name)
		if errors.Is(err, unix.ENODATA) || errors.Is(err, unix.ENOTSUP) {
			return nil
		}
		return err
	}
	if err != nil {
		return err
	}
	data := make([]byte, n)
	n, err = unix.Fgetxattr(int(src.Fd()), name, data)
	if err != nil {
		return err
	}
	return unix.Fsetxattr(int(dst.Fd()), name, data[:n], 0)
}
