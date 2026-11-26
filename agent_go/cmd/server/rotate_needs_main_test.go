package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

// sessions action=new archives the caller's Main and starts a fresh one. A Crew without a Main has nothing to start
// again: it must not create one (optional Main, PLAT-746-966), and must say what to do instead.
func TestNewChatWithoutMainCreatesNoMain(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	entries := func() int {
		doc, err := defaultProductConversationRegistryStore().loadDocument(context.Background(), productConversationRegistryPath(owner.UserID))
		if err != nil {
			t.Fatal(err)
		}
		return len(doc.Entries)
	}
	rows := func() int {
		return len(env.api.crewChats(httptest.NewRequest("GET", "/", nil), owner.UserID, "beta", linkBetaPath))
	}
	before, listed := entries(), rows()
	req := httptest.NewRequest("POST", "/api/external/call", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, owner))
	rec := httptest.NewRecorder()
	env.api.externalCrewChatsCall(rec, req, map[string]any{"crew_id": "beta", "action": "new"})
	if after := entries(); after != before || rows() != listed {
		t.Fatalf("new on a Crew with no Main created a chat: status %d %s, registry %d -> %d, listed %d -> %d", rec.Code, rec.Body.String(), before, after, listed, rows())
	}
	if rec.Code < 400 || !strings.Contains(rec.Body.String(), "Main") {
		t.Fatalf("new on a Crew with no Main should say there is no Main to start again: %d %s", rec.Code, rec.Body.String())
	}
}
