package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Owner, 3 Nov (PLAT-856-509): older synced Crews stop syncing and stay where they live. An unfinished background
// upload is moved aside (kept as a backup), an unfinished move is kept so it can be finished.
func TestMigrateCrewSyncToRelayStopsSyncingAndKeepsWork(t *testing.T) {
	state := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	write := func(id string, b crewroot.SyncBinding) {
		t.Helper()
		if err := crewroot.ReplaceSync(id, b); err != nil {
			t.Fatal(err)
		}
	}
	write("synced", crewroot.SyncBinding{Mode: "continuous", PairID: "p1", Side: "local", Editing: "local", PeerProjectID: "s1", PendingAction: "update"})
	write("moving", crewroot.SyncBinding{Mode: "manual", PairID: "p2", Side: "local", Editing: "server", PeerProjectID: "s2", PendingAction: "server"})
	write("off", crewroot.SyncBinding{Mode: "off", PairID: "p3", Side: "server", Editing: "local", PeerProjectID: "m3"})
	pending, err := crewSyncManifestPath("synced")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(pending+".pending", []byte(`{"action":"push"}`), 0600); err != nil {
		t.Fatal(err)
	}

	migrateCrewSyncToRelay()

	for id, want := range map[string]crewroot.SyncBinding{
		"synced": {Editing: "local"},
		"moving": {Editing: "server", PendingAction: "server"},
		"off":    {Editing: "local"},
	} {
		b, err := crewroot.ReadSync(id)
		if err != nil || b == nil || b.Mode != "relay" || b.Editing != want.Editing || b.PendingAction != want.PendingAction {
			t.Fatalf("%s after migration = %+v, %v", id, b, err)
		}
	}
	if _, err = os.Stat(pending + ".pending"); !os.IsNotExist(err) {
		t.Fatal("the unfinished upload is still pending")
	}
	entries, _ := os.ReadDir(filepath.Dir(pending))
	var stage, bindings int
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasPrefix(name, "synced-files.json.pending.") && strings.HasSuffix(name, ".bak"):
			stage++
		case strings.HasSuffix(name, ".bak") && strings.Contains(name, ".json."):
			bindings++
		}
	}
	if stage != 1 || bindings != 3 {
		t.Fatalf("backups: stage=%d bindings=%d, want 1 and 3", stage, bindings)
	}
}
