package handlers

import (
	"os"
	"path/filepath"
	"testing"
)

// A bound Crew whose private .sandbox-cache exists without home/ must get the home created: the sandbox policy refuses a missing
// write path (terminal showed "Disconnected").
func TestEnsureCrewShellHomeCreatesMissingHome(t *testing.T) {
	private := t.TempDir()
	if err := os.MkdirAll(filepath.Join(private, ".sandbox-cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	home, err := ensureCrewShellHome(private)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(home); err != nil || !info.IsDir() {
		t.Fatalf("home not created: %v", err)
	}
}
