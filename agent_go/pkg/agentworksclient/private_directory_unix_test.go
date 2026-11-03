//go:build !windows

package agentworksclient

import (
	"os"
	"path/filepath"
	"testing"
)

// A Crew's sync folders on a slot server are 2770 for the owner's slot group; apply refused them as "not private"
// (PLAT-856-509). That layout is accepted unchanged; a folder open to others, or group-shared without the slot
// layout, is still refused.
func TestEnsurePrivateDirectoryAcceptsOnlyTheSlotLayout(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode os.FileMode
		ok   bool
	}{
		{"slot group 2770", 0o770 | os.ModeSetgid, true},
		{"open to others 2777", 0o777 | os.ModeSetgid, false},
		{"group without setgid 0770", 0o770, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "file-snapshots")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, tc.mode); err != nil {
				t.Fatal(err)
			}
			err := EnsurePrivateDirectory(dir)
			if (err == nil) != tc.ok {
				t.Fatalf("EnsurePrivateDirectory(%v) = %v, want ok=%v", tc.mode, err, tc.ok)
			}
			if info, _ := os.Stat(dir); info.Mode()&(os.ModePerm|os.ModeSetgid) != tc.mode {
				t.Fatalf("mode changed to %v", info.Mode())
			}
		})
	}
}
