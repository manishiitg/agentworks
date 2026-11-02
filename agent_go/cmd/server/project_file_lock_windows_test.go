package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filelock"
)

// PLAT-874/450: the real Windows lock must exclude other handles, admit them
// after release, and refuse a reparse point instead of opening its target.
func TestWindowsProjectLocksRejectLinksAndExcludeOtherHandles(t *testing.T) {
	root := t.TempDir()
	first, err := openProjectOwnerLock(filepath.Join(root, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := lockProjectFile(first); err != nil {
		t.Fatal(err)
	}
	defer filelock.Unlock(first)
	second, err := openProjectOwnerLock(filepath.Join(root, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if locked, err := filelock.TryLock(second); err != nil || locked {
		t.Fatalf("another handle bypassed the owner lock: locked=%t err=%v", locked, err)
	}
	filelock.Unlock(first)
	if locked, err := filelock.TryLock(second); err != nil || !locked {
		t.Fatalf("released owner lock remained unavailable: locked=%t err=%v", locked, err)
	}
	filelock.Unlock(second)
	victim := filepath.Join(root, "victim.json")
	if err := os.WriteFile(victim, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked.lock")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatalf("could not set up reparse-point refusal case: %v", err)
	}
	if file, err := openProjectOwnerLock(link); err == nil {
		file.Close()
		t.Fatal("owner lock followed a reparse point")
	}
	if content, err := os.ReadFile(victim); err != nil || string(content) != "keep" {
		t.Fatalf("reparse target changed: %q %v", content, err)
	}
}
