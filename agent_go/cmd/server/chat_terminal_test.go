package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// PLAT-856-569: read access to another owner's chat is not export authority;
// the original owner can always supersede a terminal handoff without attach.
func TestChatTerminalOwnerRecoverySupersedesOldSession(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	crewOwnerTabsFixture(t, env, "other")
	profile, e := env.svc.registry.Resolve("work", 0, "owner")
	if e != nil {
		t.Fatal(e)
	}
	ctx := internalBotRequestContext(t.Context(), "owner")
	binding, _, e := resolveConversationBindingForUser(ctx, "owner", profile, "beta:chat:terminal-own")
	if e != nil {
		t.Fatal(e)
	}
	store := defaultProductConversationRegistryStore()
	initial, e := store.resolveOrCreate(ctx, "owner", profile, binding, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = setCodeTabName(ctx, "owner", initial.ConversationKey, "Own chat", "work"); e != nil {
		t.Fatal(e)
	}
	doc, e := store.loadDocument(ctx, productConversationRegistryPath("owner"))
	if e != nil {
		t.Fatal(e)
	}
	var source ProductConversationRecord
	for k, record := range doc.Entries {
		if record.ResourceID == "beta" || record.ConversationKey == "beta" || crewResourceProjectID(record.ConversationKey) == "beta" {
			record.Provider = "claude-code"
			doc.Entries[k] = record
			source = record
			break
		}
	}
	if source.SessionID == "" {
		t.Fatal("missing own source tab")
	}
	if e = store.writeDocument(ctx, productConversationRegistryPath("owner"), doc); e != nil {
		t.Fatal(e)
	}
	marker := chatTerminalState{Owner: "owner", Chat: source.ConversationKey, Session: source.SessionID, Native: "11111111-1111-4111-8111-111111111111", Receipt: "original", Out: true}
	if e = saveChatTerminal(marker); e != nil {
		t.Fatal(e)
	}
	call := func(user, method, op string, claims *UserClaims) *httptest.ResponseRecorder {
		q := url.Values{"conversation_key": {source.ConversationKey}, "action": {op}}
		r := httptest.NewRequest(method, "/api/external/v1/chats/terminal?"+q.Encode(), nil)
		if claims == nil {
			claims = &UserClaims{UserID: user}
		}
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
		w := httptest.NewRecorder()
		env.api.handleChatTerminal(w, r)
		return w
	}
	if w := call("owner", "GET", "status", nil); w.Code != 200 {
		t.Fatalf("owner status %d %s", w.Code, w.Body.String())
	}
	if w := call("other", "POST", "fetch", nil); w.Code != 403 {
		t.Fatalf("peer exported: %d %s", w.Code, w.Body.String())
	}
	if w := call("owner", "POST", "here", &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, CrewIDs: []string{"beta"}}}); w.Code != 403 {
		t.Fatalf("read grant changed handoff: %d", w.Code)
	}
	if chatTerminalTurnRefusal(source.SessionID) == "" {
		t.Fatal("terminal-out chat allowed a platform turn")
	}
	env.api.activeSessions = map[string]*ActiveSessionInfo{source.SessionID: {Status: "running"}}
	if w := call("owner", "POST", "here", nil); w.Code != 409 {
		t.Fatalf("busy recovery: %d %s", w.Code, w.Body.String())
	}
	env.api.activeSessions = map[string]*ActiveSessionInfo{}
	if w := call("owner", "POST", "here", nil); w.Code != 200 {
		t.Fatalf("owner recovery: %d %s", w.Code, w.Body.String())
	}
	after, e := readChatTerminal(source.SessionID)
	if e != nil || after.Out || after.Receipt == marker.Receipt {
		t.Fatalf("old handoff retained: %+v %v", after, e)
	}
	if chatTerminalTurnRefusal(source.SessionID) != "" {
		t.Fatal("recovered chat still locked")
	}
	if w := call("owner", "POST", "return", nil); w.Code != 409 {
		t.Fatalf("old terminal return: %d %s", w.Code, w.Body.String())
	}
	// An interrupted receipt must not hide the only recovery button.
	p, _ := chatTerminalPath(source.SessionID)
	if e = os.WriteFile(p, []byte("{"), 0600); e != nil {
		t.Fatal(e)
	}
	w := call("owner", http.MethodGet, "status", nil)
	var status map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &status)
	if w.Code != 200 || status["out"] != true {
		t.Fatalf("corrupt receipt hid recovery: %d %s", w.Code, w.Body.String())
	}
	if w = call("owner", http.MethodPost, "here", nil); w.Code != 200 {
		t.Fatalf("corrupt receipt recovery: %d %s", w.Code, w.Body.String())
	}
}
