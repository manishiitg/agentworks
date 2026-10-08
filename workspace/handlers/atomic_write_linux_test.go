package handlers

import (
	"bytes"
	"encoding/binary"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestAtomicWriteRetainsLinuxSlotOwnershipAndACL(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to create a slot-owned fixture")
	}
	file := filepath.Join(t.TempDir(), "slot.txt")
	if err := os.WriteFile(file, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(file, 12345, 12346); err != nil {
		t.Fatal(err)
	}
	acl := []byte{2, 0, 0, 0}
	for _, entry := range []struct {
		tag, perm uint16
		id        uint32
	}{{1, 6, 0xffffffff}, {2, 6, 12347}, {4, 4, 0xffffffff}, {16, 6, 0xffffffff}, {32, 0, 0xffffffff}} {
		b := make([]byte, 8)
		binary.LittleEndian.PutUint16(b, entry.tag)
		binary.LittleEndian.PutUint16(b[2:], entry.perm)
		binary.LittleEndian.PutUint32(b[4:], entry.id)
		acl = append(acl, b...)
	}
	if err := unix.Setxattr(file, "system.posix_acl_access", acl, 0); errors.Is(err, unix.ENOTSUP) {
		t.Skip("filesystem has no ACL support")
	} else if err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(file, []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(file)
	owner := info.Sys().(*syscall.Stat_t)
	if owner.Uid != 12345 || owner.Gid != 12346 || info.Mode().Perm() != 0660 {
		t.Fatalf("slot metadata lost: uid=%d gid=%d mode=%v", owner.Uid, owner.Gid, info.Mode())
	}
	got := make([]byte, 128)
	n, err := unix.Getxattr(file, "system.posix_acl_access", got)
	if err != nil || !bytes.Equal(got[:n], acl) {
		t.Fatalf("ACL lost: %x %v", got[:n], err)
	}
}
