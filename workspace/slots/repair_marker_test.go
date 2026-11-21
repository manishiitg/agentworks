package slots

import (
	"os"
	"path/filepath"
	"testing"
)

// #3156: the server moves its accounts out of users.json only once the root
// slot-repair timer reads them from the state database; an older timer would
// stop giving new people a slot.
func TestRepairReadsServerStateNeedsTheNewTimerMarker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvTableFile, filepath.Join(dir, "slots.json"))
	t.Setenv(EnvEnabled, "")
	if !RepairReadsServerState() {
		t.Fatal("a host without slots has no timer to wait for")
	}
	t.Setenv(EnvEnabled, "on")
	if RepairReadsServerState() {
		t.Fatal("slotted host without the marker must wait")
	}
	if err := os.WriteFile(filepath.Join(dir, "slot-repair.json"), []byte(`{"user_directory":"server-state"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !RepairReadsServerState() {
		t.Fatal("marker written by install-timer not recognised")
	}
}
