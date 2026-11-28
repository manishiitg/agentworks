package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// A chat deletes only a fork or helper it made that is still under it in the left menu. Once the person moves the fork
// out (forked_from no longer the chat), or for Main, itself, or another agent's fork, it is refused.
func TestChatDeletesOnlyItsOwnStillNestedFork(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	ctx := context.WithValue(context.Background(), UserContextKey, owner)
	open := func(name string) string {
		req := httptest.NewRequest("POST", "/api/external/call", nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		env.api.externalCrewChatsCall(rec, req, map[string]any{"crew_id": "beta", "action": "side_open", "name": name})
		out := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		id, _ := out["chat_id"].(string)
		if rec.Code != 200 || id == "" {
			t.Fatalf("open %s: %d %s", name, rec.Code, rec.Body.String())
		}
		return "beta" + codeChatSideMarker + id
	}
	parent, child, other := open("Parent"), open("Child"), open("Other")
	store := defaultProductConversationRegistryStore()
	setParent := func(key, to string) {
		if _, err := store.setChatContextOptions(ctx, owner.UserID, "work", key, chatContextChange{ForkedFrom: &to}, owner.UserID); err != nil {
			t.Fatal(err)
		}
	}
	setParent(child, parent)
	setParent(other, child)
	project := codeChatProject{OwnerID: owner.UserID, ProfileID: "work", ProjectID: "beta", Self: codeChat{Key: parent, OwnerID: owner.UserID}}
	chat := func(key string) codeChat { return codeChat{Key: key, Name: key, OwnerID: owner.UserID} }

	if reason := childForkRefusal(ctx, owner.UserID, project, chat(child)); reason != "" {
		t.Fatalf("parent could not delete its own nested fork: %s", reason)
	}
	for name, tc := range map[string]codeChat{
		"main":                 {Key: "beta", IsDefaultAgent: true, OwnerID: owner.UserID},
		"itself":               chat(parent),
		"another agent's fork": chat(other),
		"someone else's tab":   {Key: child, OwnerID: "teammate"},
	} {
		if reason := childForkRefusal(ctx, owner.UserID, project, tc); reason == "" {
			t.Errorf("%s: deletion was allowed", name)
		}
	}
	// The person moves the fork out from under its parent in the left menu: it is no longer the parent's to delete.
	setParent(child, "")
	if reason := childForkRefusal(ctx, owner.UserID, project, chat(child)); !strings.Contains(reason, "ask the person") {
		t.Fatalf("a fork moved out of its parent could still be deleted by it: %q", reason)
	}
}

// Another person's ask runs in the agent's own chat with its owner's authority (#3471), so the turn decides: a
// teammate's ask may not delete the owner's forks, whatever the agent's Make changes setting; the owner's turn may.
func TestOnlyTheOwnersOwnTurnDeletesForks(t *testing.T) {
	api := &StreamingAPI{lastQueryRequests: map[string]QueryRequest{}}
	if !api.anotherPersonsTurn("chat", "owner") {
		t.Fatal("a turn of unknown origin was treated as the owner's")
	}
	api.lastQueryRequests["chat"] = QueryRequest{SharedAgentSender: "teammate"}
	if !api.anotherPersonsTurn("chat", "owner") {
		t.Fatal("a teammate's shared ask could delete the owner's forks")
	}
	api.lastQueryRequests["chat"] = QueryRequest{AgentMessageFromOtherPerson: true}
	if !api.anotherPersonsTurn("chat", "owner") {
		t.Fatal("another person's agent message could delete the owner's forks")
	}
	api.lastQueryRequests["chat"] = QueryRequest{}
	if api.anotherPersonsTurn("chat", "owner") {
		t.Fatal("the owner's own turn was refused")
	}
}
