package handlers

import (
	"bytes"
	"encoding/binary"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
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

func TestAtomicWriteRetainsRootlessSlotAccess(t *testing.T) {
	if file := os.Getenv("AGENTWORKS_ROOTLESS_FILE_FIXTURE"); file != "" {
		switch os.Getenv("AGENTWORKS_ROOTLESS_FILE_ROLE") {
		case "service":
			if err := writeFileAtomic(file, []byte("service edit"), 0644); err != nil {
				t.Fatal(err)
			}
		case "slot":
			if err := os.WriteFile(file, []byte("slot edit"), 0644); err != nil {
				t.Fatal(err)
			}
		case "limited":
			if _, err := os.ReadFile(file); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("unauthorized edit"), 0644); err == nil {
				t.Fatal("masked read-only ACL widened")
			}
		}
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root only to create fixtures and start unprivileged test processes")
	}
	base, err := os.MkdirTemp("", "rootless-slot-write-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	if err := os.Chown(base, 0, 12346); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0771); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(base, "slot.txt")
	if err := os.WriteFile(file, []byte("original"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(file, 12345, 12346); err != nil {
		t.Fatal(err)
	}
	acl := []byte{2, 0, 0, 0}
	for _, e := range []struct {
		tag, perm uint16
		id        uint32
	}{{1, 6, 0xffffffff}, {2, 6, 34567}, {4, 4, 0xffffffff}, {16, 4, 0xffffffff}, {32, 0, 0xffffffff}} {
		b := make([]byte, 8)
		binary.LittleEndian.PutUint16(b, e.tag)
		binary.LittleEndian.PutUint16(b[2:], e.perm)
		binary.LittleEndian.PutUint32(b[4:], e.id)
		acl = append(acl, b...)
	}
	if err := unix.Setxattr(file, "system.posix_acl_access", acl, 0); errors.Is(err, unix.ENOTSUP) {
		t.Skip("filesystem has no ACL support")
	} else if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(role string, uid, gid uint32) {
		t.Helper()
		cmd := exec.Command(binary, "-test.run", "^TestAtomicWriteRetainsRootlessSlotAccess$")
		cmd.Env = append(os.Environ(), "AGENTWORKS_ROOTLESS_FILE_FIXTURE="+file, "AGENTWORKS_ROOTLESS_FILE_ROLE="+role)
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid}}
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v %s", role, err, output)
		}
	}
	run("service", 23456, 12346)
	run("limited", 34567, 34568)
	run("slot", 12345, 12346)
	if data, _ := os.ReadFile(file); string(data) != "slot edit" {
		t.Fatalf("slot edit lost: %s", data)
	}
	// The ACL also survives the next service-owned replacement.
	run("service", 23456, 12346)
	run("limited", 34567, 34568)
	run("slot", 12345, 12346)
}
