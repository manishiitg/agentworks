package server

import (
	"context"
	"testing"
)

// PLAT-746-852: a message from the agent owner's own account (their agent on another computer or login) is the
// owner's, not answer-only; another person's message keeps the Team rule.
func TestAgentRuleArgsMarkOnlyTheOwnersOwnAgent(t *testing.T) {
	own := withAgentRuleArgs(context.Background(), map[string]any{"sender_id": "owner-1"}, "", "owner-1", "")
	if own["own"] != true {
		t.Fatalf("owner's own agent not marked own: %v", own)
	}
	other := withAgentRuleArgs(context.Background(), map[string]any{"sender_id": "colleague-2"}, "", "owner-1", "")
	if _, marked := other["own"]; marked || other["can_make_changes"] != false {
		t.Fatalf("another person's agent must stay on the Team rule (answer only by default): %v", other)
	}
}
