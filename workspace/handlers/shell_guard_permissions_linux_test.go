//go:build linux

package handlers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSlotWriteDirectoryPermissions(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Workflow", "test", "run", "extract")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	readOnly := filepath.Join(root, "read-only")
	if err := os.Mkdir(readOnly, 0750); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(readOnly)
	if err := prepareGuardWriteDirectory(path, root, true); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm()&0070 != 0070 || info.Mode().Perm()&0007 != 0 || info.Mode()&os.ModeSetgid == 0 {
		t.Fatalf("write target mode = %v", info.Mode())
	}
	for parent := filepath.Dir(path); parent != root; parent = filepath.Dir(parent) {
		info, _ := os.Stat(parent)
		if info.Mode().Perm()&0010 == 0 || info.Mode().Perm()&0020 != 0 {
			t.Fatalf("ancestor mode = %v", info.Mode())
		}
	}
	after, _ := os.Stat(readOnly)
	if after.Mode() != before.Mode() {
		t.Fatal("changed a read-only directory")
	}
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := makeSlotWriteDirectoryUsable(link, root); err == nil {
		t.Fatal("accepted escaped path")
	}
}
