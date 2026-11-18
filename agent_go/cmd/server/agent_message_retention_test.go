package server

import (
	"testing"
	"time"
)

// #3157: local pending messages have the same 24-hour delivery expiry as
// connected installs. Expiry must retain history and delivered receipts.
func TestLocalPendingMessageExpiryKeepsHistory(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	now := time.Now().UTC()
	store := agentMessageStore{Conversations: []agentConversation{{ID: "inbox-local", Messages: []agentMessage{
		{ID: "old", Text: "fixture note", Delivery: "pending", SentAt: now.Add(-25 * time.Hour)},
		{ID: "boundary", Delivery: "stored", SentAt: now.Add(-24 * time.Hour)},
		{ID: "received", Delivery: "delivered", SentAt: now.Add(-25 * time.Hour)},
	}}}}
	if got := expirePendingAgentMessages(&store, now); got != 1 {
		t.Fatalf("expired %d pending messages, want 1", got)
	}
	messages := store.Conversations[0].Messages
	if len(messages) != 3 || messages[0].Delivery != "expired" || messages[0].Text != "fixture note" || messages[1].Delivery != "stored" || messages[2].Delivery != "delivered" {
		t.Fatal("expiry removed history or changed an unexpired/delivered message")
	}
	t.Log("[AGENTBRIDGE] event=local_contract pending_expiry=24h history=retained")
}
