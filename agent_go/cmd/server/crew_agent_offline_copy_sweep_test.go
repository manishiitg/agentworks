package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #3035: failed offline-copy builds left full journal copies behind; only old offline-copy-* stages are swept.
func TestCrewOfflineCopyStageSweepRemovesOnlyOldStages(t *testing.T) {
	private := t.TempDir()
	old := filepath.Join(private, "offline-copy-old")
	fresh := filepath.Join(private, "offline-copy-fresh")
	other := filepath.Join(private, "chats")
	for _, dir := range []string{old, fresh, other} {
		if err := os.MkdirAll(filepath.Join(dir, "source"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-3 * crewOfflineCopyStaleStage)
	for _, dir := range []string{old, other} {
		if err := os.Chtimes(dir, past, past); err != nil {
			t.Fatal(err)
		}
	}
	sweepCrewOfflineCopyStages(private)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old stage was not removed")
	}
	for _, dir := range []string{fresh, other} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("%s should stay: %v", filepath.Base(dir), err)
		}
	}
}
