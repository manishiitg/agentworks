package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

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
	// While the Team is on, Mac online stays on (owner, 6 Nov): the owner turns the Team off first.
	team := crewTeam{ProjectID: twin, OwnerID: owner.UserID, On: true}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	if w := unpair(owner, twin, pair); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "Turn the Team off first") {
		t.Fatalf("Reach off went through while the Team is on: %d %s", w.Code, w.Body.String())
	}
	team.On = false
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
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
