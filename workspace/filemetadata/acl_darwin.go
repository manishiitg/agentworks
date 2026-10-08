package filemetadata

import (
	"os"
	"syscall"
)

// macOS grants are owned by the signed-in CLI user; Unix ownership/mode is
// preserved here. Server slot-account ACL preservation is Linux-specific.
func preserveACL(dst, src *os.File) error { return nil }

func preserveUnownedFile(dst, src *os.File, info os.FileInfo, uid, gid uint32) error {
	return syscall.EPERM
}
