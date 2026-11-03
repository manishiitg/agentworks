package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
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

// Owner, 3 Nov: the owner's AI app talks in the Crew's Main chat but reads back only its own turns, never the rest of
// the owner's Main history.
func TestOwnerAppReadsOnlyItsOwnTurnsInMain(t *testing.T) {
	items := []codeTurnItem{
		{Turn: "t1", Role: "you", Text: "private plan for the launch"},
		{Turn: "t1", Role: "assistant", Text: "owner's answer"},
		{Turn: "t2", Role: "you", Sender: "via ChatGPT", Text: "what changed today?"},
		{Turn: "t2", Role: "assistant", Text: "app's answer"},
		{Turn: "t3", Role: "you", Text: "queued app question"},
		{Turn: "t3", Role: "assistant", Text: "queued answer"},
	}
	got := ownerAppTurns(items, "via ChatGPT", "", "queued app question")
	if len(got) != 4 {
		t.Fatalf("got %d items, want the app's two turns: %+v", len(got), got)
	}
	for _, item := range got {
		if item.Turn == "t1" {
			t.Fatalf("the app saw the owner's own Main turn: %+v", item)
		}
	}
}

// Reach off removes a Mac Crew's empty online copy (owner, 3 Nov). Only its owner, with the pair the Mac holds, can;
// a Crew that lives online is never removed this way; the copy is backed up first.
func checkCrewReachOff(t *testing.T, api *StreamingAPI, owner *UserClaims, twin, pair, movedOnline, movedPair string) {
	t.Helper()
	unpair := func(who *UserClaims, project, pair string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(crewSyncRequest{Action: "unpair", PairID: pair})
		r := httptest.NewRequest(http.MethodPost, "/api/crew-sync/"+project, bytes.NewReader(body))
		r = mux.SetURLVars(r.WithContext(context.WithValue(t.Context(), UserContextKey, who)), map[string]string{"project": project})
		w := httptest.NewRecorder()
		api.handleCrewSync(w, r)
		return w
	}
	if w := unpair(crewWriter("other"), twin, pair); w.Code < 400 {
		t.Fatalf("another person removed the online copy: %d", w.Code)
	}
	if w := unpair(owner, twin, "wrong-pair"); w.Code < 400 {
		t.Fatalf("a wrong pair removed the online copy: %d", w.Code)
	}
	if w := unpair(owner, movedOnline, movedPair); w.Code < 400 {
		t.Fatalf("Reach off removed a Crew that lives online: %d", w.Code)
	}
	if w := unpair(owner, twin, pair); w.Code != http.StatusOK {
		t.Fatalf("owner could not turn Reach off: %d %s", w.Code, w.Body.String())
	}
	if b, _ := crewroot.ReadSync(twin); b != nil {
		t.Fatal("the online copy's pair survived Reach off")
	}
	state, _ := crewroot.StateRoot()
	if backups, _ := filepath.Glob(filepath.Join(state, "crew-backups", twin+"-*.tar.gz")); len(backups) != 1 {
		t.Fatalf("no backup before removing the online copy: %v", backups)
	}
}
