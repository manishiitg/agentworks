package workflowfiles

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestGuardedWriteRetainsLinuxSlotOwnership(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to create a slot-owned fixture")
	}
	e, root, _ := testEditor(t)
	file := filepath.Join(root, "slot.txt")
	if err := os.WriteFile(file, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(file, 12345, 12346); err != nil {
		t.Fatal(err)
	}
	_, err := e.Write(t.Context(), WriteRequest{Root: ".", Path: "slot.txt", Content: "new", Actor: "owner", RequestID: "edit", ExpectedRevision: Revision([]byte("old"))})
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(file)
	owner := info.Sys().(*syscall.Stat_t)
	if owner.Uid != 12345 || owner.Gid != 12346 || info.Mode().Perm() != 0644 {
		t.Fatalf("slot metadata lost: uid=%d gid=%d mode=%v", owner.Uid, owner.Gid, info.Mode())
	}
}
