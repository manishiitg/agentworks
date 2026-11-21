package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// #3504: the person moves a fork out from under its agent (and may put it back). Only their own agents, never Main,
// never into a loop; once moved out, the agent it was under can no longer delete it (#3500).
func TestPersonMovesAForkOutAndBack(t *testing.T) {
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
	parent, child := open("Parent"), open("Child")
	store := defaultProductConversationRegistryStore()
	if _, err := store.setChatContextOptions(ctx, owner.UserID, "work", child, chatContextChange{ForkedFrom: &parent}, owner.UserID); err != nil {
		t.Fatal(err)
	}
	move := func(user, key, under string) int {
		status, _ := env.api.setForkParent(ctx, user, "work", "beta", linkBetaPath, key, under)
		return status
	}
	forkedFrom := func(key string) string {
		identity, err := store.agentIdentity(ctx, owner.UserID, "work", key)
		if err != nil {
			t.Fatal(err)
		}
		return identity.ForkedFrom
	}
	project := codeChatProject{OwnerID: owner.UserID, ProfileID: "work", ProjectID: "beta", Self: codeChat{Key: parent, OwnerID: owner.UserID}}
	fork := codeChat{Key: child, Name: "Child", OwnerID: owner.UserID}

	if status := move(owner.UserID, child, ""); status != 0 || forkedFrom(child) != "" {
		t.Fatalf("moving the fork out: status %d, still under %q", status, forkedFrom(child))
	}
	if reason := childForkRefusal(ctx, owner.UserID, project, fork); reason == "" {
		t.Fatal("the agent it was under could still delete the fork after the person moved it out")
	}
	if status := move(owner.UserID, child, parent); status != 0 || forkedFrom(child) != parent {
		t.Fatalf("putting it back: status %d, under %q", status, forkedFrom(child))
	}
	if reason := childForkRefusal(ctx, owner.UserID, project, fork); reason != "" {
		t.Fatalf("put back under its agent, the fork was not its child again: %s", reason)
	}
	for name, status := range map[string]int{
		"main":                 move(owner.UserID, "beta", ""),
		"under itself":         move(owner.UserID, child, child),
		"under its own fork":   move(owner.UserID, parent, child),
		"another Crew":         move(owner.UserID, child, "alpha"+codeChatSideMarker+"x"),
		"a teammate moving it": move("teammate", child, ""),
	} {
		if status == 0 {
			t.Errorf("%s was allowed", name)
		}
	}
	if forkedFrom(child) != parent || forkedFrom(parent) != "" {
		t.Fatalf("a refused move changed the menu: child under %q, parent under %q", forkedFrom(child), forkedFrom(parent))
	}
}
