//go:build unix

package browserconfig

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// PLAT-847/847-2: a predictable deployment parent in /tmp must refuse
// pre-created unsafe directories, including each descendant used for IPC.
func TestManagedDirectoriesRejectUnsafeExistingPaths(t *testing.T) {
	for _, level := range []int{0, 1, 2, 3} {
		for _, kind := range []string{"broad-mode", "symlink", "foreign-owner"} {
			t.Run(fmt.Sprintf("level-%d/%s", level, kind), func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "deployment")
				parts := []string{"o", "owner", "artifacts"}
				if err := ensurePrivateDirectories(root, parts...); err != nil {
					t.Fatal(err)
				}
				unsafe := root
				for _, name := range parts[:level] {
					unsafe = filepath.Join(unsafe, name)
				}
				switch kind {
				case "broad-mode":
					if err := os.Chmod(unsafe, 0777); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					if err := os.RemoveAll(unsafe); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(t.TempDir(), unsafe); err != nil {
						t.Fatal(err)
					}
				case "foreign-owner":
					chown := func(uid int) error {
						if os.Geteuid() == 0 {
							return os.Chown(unsafe, uid, -1)
						}
						return exec.Command("sudo", "-n", "chown", fmt.Sprint(uid), unsafe).Run()
					}
					foreignUID := 65534
					if os.Geteuid() == foreignUID {
						foreignUID = 0
					}
					if err := chown(foreignUID); err != nil {
						t.Skipf("foreign owner fixture requires chown: %v", err)
					}
					t.Cleanup(func() {
						if err := chown(os.Geteuid()); err != nil {
							t.Errorf("restore fixture ownership: %v", err)
						}
					})
				}
				if err := ensurePrivateDirectories(root, parts...); err == nil {
					t.Fatal("accepted an unsafe existing browser directory")
				}
				if kind == "broad-mode" {
					if info, err := os.Stat(unsafe); err != nil || info.Mode().Perm() != 0777 {
						t.Fatalf("refusal must not repair existing permissions: %v %v", info, err)
					}
				}
			})
		}
	}
}
