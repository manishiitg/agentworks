package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// PLAT-856-497: Off must work with a transfer lock held and the peer offline;
// a finishing transfer cannot re-enable it, and the next contact informs peer.
func checkCrewSyncOff(t *testing.T, api *StreamingAPI, ctx context.Context, local, server crewroot.Roots, offline *atomic.Bool) {
	t.Helper()
	request := func(action, mode, user string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]string{"action": action, "mode": mode})
		r := desktopTestRequest("POST", "/", string(raw), user)
		r = mux.SetURLVars(r, map[string]string{"project": local.ID, "connection": "saved"})
		w := httptest.NewRecorder()
		api.handleDesktopCrewSync(w, r)
		return w
	}
	stale, err := crewroot.ReadSync(local.ID)
	if err != nil {
		t.Fatal(err)
	}
	offline.Store(true)
	unlock, err := wf.LockWorkspace(ctx, local.Files)
	if err != nil {
		t.Fatal(err)
	}
	w := request("mode", "off", "owner")
	unlock()
	if w.Code != 200 || !strings.Contains(w.Body.String(), "could not be told") {
		t.Fatalf("offline Off: %d %s", w.Code, w.Body.String())
	}
	if w := request("mode", "off", "other"); w.Code != 403 {
		t.Fatalf("another person changed Off: %d %s", w.Code, w.Body.String())
	}
	stale.Editing = "server"
	if err := crewroot.WriteSync(local.ID, *stale); err != nil {
		t.Fatal(err)
	}
	b, err := crewroot.ReadSync(local.ID)
	if err != nil || b.Mode != "off" || b.PairID != stale.PairID || b.SplitFiles != stale.SplitFiles {
		t.Fatalf("transfer changed Off/layout: %+v %v", b, err)
	}
	if locked, err := local.SourceLocked(); err != nil || locked {
		t.Fatalf("Off kept source locked: %t %v", locked, err)
	}
	if err := crewSyncOn(local.ID); err == nil {
		t.Fatal("new transfer admitted while Off")
	}
	if w := request("update", "", "owner"); w.Code != 409 {
		t.Fatalf("Off update: %d", w.Code)
	}
	offline.Store(false)
	if w := request("status", "", "owner"); w.Code != 200 {
		t.Fatalf("Off status retry: %d %s", w.Code, w.Body.String())
	}
	peer, err := crewroot.ReadSync(server.ID)
	if err != nil || peer.Mode != "off" {
		t.Fatalf("next contact did not tell peer: %+v %v", peer, err)
	}
	if w := request("mode", "continuous", "owner"); w.Code != 200 {
		t.Fatalf("On: %d %s", w.Code, w.Body.String())
	}
	for _, id := range []string{local.ID, server.ID} {
		b, err := crewroot.ReadSync(id)
		if err != nil || b.Mode != "continuous" {
			t.Fatalf("On failed: %+v %v", b, err)
		}
	}
}
