package filemetadata

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"

	"golang.org/x/sys/unix"
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

// A rootless service cannot assign another UID. Keep the original group (slot
// services already belong to their slot groups) and express the old owner's
// access as a named POSIX ACL. Normalize masked entries before enlarging the
// mask, so granting the former owner does not widen anyone else's permissions.
func preserveUnownedFile(dst, src *os.File, info os.FileInfo, uid, gid uint32) error {
	if err := dst.Chown(-1, int(gid)); err != nil {
		return err
	}
	if err := dst.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	type entry struct {
		tag, perm uint16
		id        uint32
	}
	mode := uint16(info.Mode().Perm())
	entries := []entry{{1, mode >> 6 & 7, ^uint32(0)}, {4, mode >> 3 & 7, ^uint32(0)}, {32, mode & 7, ^uint32(0)}}
	const attribute = "system.posix_acl_access"
	n, err := unix.Fgetxattr(int(src.Fd()), attribute, nil)
	if err != nil && !errors.Is(err, unix.ENODATA) {
		return err
	}
	if err == nil {
		data := make([]byte, n)
		n, err = unix.Fgetxattr(int(src.Fd()), attribute, data)
		if err != nil {
			return err
		}
		if n < 4 || (n-4)%8 != 0 || binary.LittleEndian.Uint32(data[:4]) != 2 {
			return fmt.Errorf("unsupported POSIX access ACL")
		}
		entries = nil
		for i := 4; i < n; i += 8 {
			entries = append(entries, entry{binary.LittleEndian.Uint16(data[i:]), binary.LittleEndian.Uint16(data[i+2:]), binary.LittleEndian.Uint32(data[i+4:])})
		}
	}
	oldMask := uint16(7)
	for _, e := range entries {
		if e.tag == 16 {
			oldMask = e.perm
		}
	}
	ownerAccess, mask := mode>>6&7, uint16(0)
	kept := entries[:0]
	for _, e := range entries {
		if e.tag == 16 || e.tag == 2 && e.id == uid {
			continue
		}
		if e.tag == 2 || e.tag == 4 || e.tag == 8 {
			e.perm &= oldMask
			mask |= e.perm
		}
		kept = append(kept, e)
	}
	kept = append(kept, entry{2, ownerAccess, uid}, entry{16, mask | ownerAccess, ^uint32(0)})
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].tag == kept[j].tag {
			return kept[i].id < kept[j].id
		}
		return kept[i].tag < kept[j].tag
	})
	data := make([]byte, 4+8*len(kept))
	binary.LittleEndian.PutUint32(data, 2)
	for i, e := range kept {
		offset := 4 + i*8
		binary.LittleEndian.PutUint16(data[offset:], e.tag)
		binary.LittleEndian.PutUint16(data[offset+2:], e.perm)
		binary.LittleEndian.PutUint32(data[offset+4:], e.id)
	}
	return unix.Fsetxattr(int(dst.Fd()), attribute, data, 0)
}
