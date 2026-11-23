package musecli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The hook folder is this user's own (so several service accounts on one
// host never collide), owner-only, and a planted symlink is refused.
func TestMuseHookDirIsPerUserAndOwnerOnly(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	dir, err := museHookDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(tmp, fmt.Sprintf("muse-cli-hooks-%d", os.Getuid())); dir != want {
		t.Fatalf("dir = %s, want %s", dir, want)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %v %v", info.Mode(), err)
	}
	if _, err := museHookDir(); err != nil {
		t.Fatalf("second call: %v", err)
	}

	planted := t.TempDir()
	t.Setenv("TMPDIR", planted)
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(planted, fmt.Sprintf("muse-cli-hooks-%d", os.Getuid()))); err != nil {
		t.Fatal(err)
	}
	if _, err := museHookDir(); err == nil {
		t.Fatal("a planted symlink was used as the hook folder")
	}
}
