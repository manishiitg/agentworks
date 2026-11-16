package server

import (
	"strings"
	"testing"
)

// #2762: old product/name peer ingress must refuse before any model turn or
// fallback resolution. The hosted reply test pins the allowed Bridge path.
func TestBridgePeerRoutesRefuseProductAndChatFallbacks(t *testing.T) {
	env := newTriggerLinkEnv(t)
	ctx := internalBotRequestContext(t.Context(), "owner")
	claims := &UserClaims{UserID: "owner"}
	for _, args := range []map[string]any{
		{"crew_id": "beta"}, {"workflow_id": "reports"},
		{"target": "Beta"}, {"target": "workspace:work:beta"},
		{"target": "agent:worker", "chat_id": "beta:chat:worker"},
	} {
		if _, err := env.api.externalMessageTarget(ctx, claims, args); err == nil {
			t.Fatalf("retired target accepted: %v", args)
		}
	}
	if code, _ := workspaceOwnerTabRequest(t, env, claims, "workspace", map[string]any{"action": "message_chat", "workspace_id": "work:beta", "message": "Must not start"}); code < 400 {
		t.Fatal("removed workspace message_chat still delivered")
	}
	if _, err := env.api.resolveBridgeMessageAgent(ctx, claims, "Beta", ""); err == nil || !strings.Contains(err.Error(), "address") {
		t.Fatalf("bare-name fallback did not refuse at Bridge boundary: %v", err)
	}
}

// Main is optional and reads never create it. These scenarios explicitly open
// the fixture owner's Main before testing a delivery, like the real Crew UI.
func bridgeRouteTestMain(t *testing.T, api *StreamingAPI, owner, project string) {
	t.Helper()
	ctx := internalBotRequestContext(t.Context(), owner)
	profile, err := api.agentProfiles.Resolve("work", 0, owner)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := resolveProductConversationBinding(ctx, owner, profile, project+":chat:fixture-main")
	if err != nil {
		t.Fatal(err)
	}
	store := defaultProductConversationRegistryStore()
	record, err := store.resolveOrCreate(ctx, owner, profile, binding, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.writeMainChatPointer(ctx, binding.ManifestPath, record.ConversationKey, record.SessionID); err != nil {
		t.Fatal(err)
	}
}
