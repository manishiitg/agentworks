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
