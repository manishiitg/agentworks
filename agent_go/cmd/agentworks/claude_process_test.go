package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// PLAT-856-360: a background worker kept its versioned install path as comm.
// The truncated path was skipped, binding hooks to the foreground ancestor.
func TestBackgroundClaudeWorkerKeepsItsOwnProcessIdentity(t *testing.T) {
	const birth = "Sat Oct 24 23:28:48 2026"
	const pid = "53223"
	for _, tc := range []struct {
		image string
		want  bool
	}{
		{"claude", true},
		{"/Users/example/.local/share/claude/versions/2.1.310", true},
		{"/Users/Example User/.local/share/claude/versions/2.1.310", true},
		{"/Users/example/.loc", false},
		{"/opt/other/versions/2.1.310", false},
		{"2.1.310", false},
	} {
		if got := ownProviderExecutable(tc.image) == "claude"; got != tc.want {
			t.Fatalf("worker executable %q: got %t, want %t", tc.image, got, tc.want)
		}
		sum := sha256.Sum256([]byte(pid + " " + birth))
		alive := providerHostAliveWith("claude", hex.EncodeToString(sum[:]), func(_ []string, _ ...string) ([]byte, error) {
			return []byte(strings.Join([]string{pid, birth, tc.image}, " ") + "\n"), nil
		})
		if alive != tc.want {
			t.Fatalf("worker liveness %q: got %t, want %t", tc.image, alive, tc.want)
		}
	}
}
