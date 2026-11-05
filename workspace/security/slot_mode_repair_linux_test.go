//go:build linux

package security

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Older slot launches left git's files owner-only (0600 HEAD/index, 0400 objects, 0700 folders), hiding the repo from
// the platform account in the slot's group. The repair gives the group the owner's bits inside .git trees only, never
// touches "other" bits, ordinary files or anything reached through a symlink.
func TestRepairOwnGitModesOnlyGitTreesGroupLikeOwner(t *testing.T) {
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	root := t.TempDir()
	outside := t.TempDir()
	mk := func(rel string, mode os.FileMode) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o770); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	head := mk("code/repo/.git/HEAD", 0o600)
	object := mk("code/repo/.git/objects/ab/cdef", 0o400)
	index := mk("code/repo/.git/worktrees/wt/index", 0o604)
	fine := mk("code/repo/.git/config", 0o664)
	work := mk("code/repo/notes.txt", 0o600)
	vendored := mk("node_modules/pkg/.git/HEAD", 0o600)
	objDir := filepath.Join(root, "code/repo/.git/objects/ab")
	if err := os.Chmod(objDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "secret")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "code/repo/.git/link")); err != nil {
		t.Fatal(err)
	}

	n, err := RepairOwnGitModes(root)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("changed %d, want 4 (HEAD, object, worktree index, objects folder)", n)
	}
	for path, want := range map[string]os.FileMode{head: 0o660, object: 0o440, index: 0o664, objDir: 0o770, fine: 0o664, work: 0o600, vendored: 0o600, target: 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s: mode %o, want %o", path, got, want)
		}
	}
}
