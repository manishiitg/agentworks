package slotfs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The newest recent last-launch.stderr names why a slot command did not start.
func TestLatestLaunchStderrLine(t *testing.T) {
	root := t.TempDir()
	write := func(slot, body string, age time.Duration) {
		dir := filepath.Join(root, slot)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "last-launch.stderr")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-age)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
	write("slot01", "old failure\n", time.Hour)
	write("slot02", "sh: 1: exec: muse: not found\nsecond line\n", time.Second)
	write("slot03", "", 30*time.Second)
	if got := latestLaunchStderrLineIn(root, 2*time.Minute, time.Now()); got != "sh: 1: exec: muse: not found" {
		t.Fatalf("hint = %q", got)
	}
	if got := latestLaunchStderrLineIn(t.TempDir(), time.Minute, time.Now()); got != "" {
		t.Fatalf("no files must give no hint, got %q", got)
	}
}
