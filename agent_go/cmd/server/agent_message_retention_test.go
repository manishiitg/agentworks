package server

import (
	"fmt"
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

// Review of #3157: cleanup at a sender's 300-conversation limit removed the
// oldest thread even when it held an unread message, and the 7-day idle sweep
// removed threads whose task was still open, so the receiver never saw the
// message and the task vanished from Team › Tasks with no way to finish it.
func TestConversationCleanupKeepsUnreadMailAndOpenTasks(t *testing.T) {
	now := time.Now().UTC()
	owner := [2]agentMessageEndpoint{{UserID: "owner", ID: "a"}, {UserID: "owner", ID: "b"}}
	at := func(id string, ago time.Duration, messages ...agentMessage) agentConversation {
		for i := range messages {
			messages[i].Sequence, messages[i].SentAt = i+1, now.Add(-ago)
		}
		return agentConversation{ID: id, Endpoints: owner, Messages: messages}
	}
	request := agentMessage{ID: "msg-task", MessageType: "request", Delivery: "delivered", From: 0}
	store := agentMessageStore{Conversations: []agentConversation{
		at("inbox-unread", 23*time.Hour, agentMessage{ID: "msg-unread", Delivery: "stored"}),
		// Codex holds this one in the chat's own queue; it is not delivered yet.
		at("inbox-codex-queued", 23*time.Hour, agentMessage{ID: "msg-queued", Delivery: agentDeliveryQueued}),
		at("inbox-open-task", 8*24*time.Hour, request),
		at("inbox-done-task", 8*24*time.Hour, request, agentMessage{ID: "msg-done", From: 1, Delivery: "delivered", ReplyStatus: "done"}),
	}}
	for i := len(store.Conversations); i < agentConversationPerUser+1; i++ {
		store.Conversations = append(store.Conversations, at(fmt.Sprintf("inbox-read-%d", i), time.Duration(i)*time.Minute, agentMessage{Delivery: "delivered"}))
	}
	pruneAgentConversations(&store, now, "owner")
	kept := map[string]bool{}
	for _, c := range store.Conversations {
		kept[c.ID] = true
	}
	if !kept["inbox-codex-queued"] {
		t.Fatal("cleanup removed a message Codex still holds in the chat's queue")
	}
	if state, _ := receiptState(agentMessage{Delivery: agentDeliveryQueued}, nil, true); state == "delivered" {
		t.Fatal("a message only queued in Codex claimed the chat has it")
	}
	if agentMessagePendingExpired(agentMessage{Delivery: agentDeliveryQueued, SentAt: now.Add(-48 * time.Hour)}, now) {
		t.Fatal("a queued message expired and would be offered to the chat again")
	}
	if !kept["inbox-unread"] || !kept["inbox-open-task"] || kept["inbox-done-task"] {
		t.Fatalf("unread=%v open_task=%v done_task=%v; want unread and open task kept, done task removed", kept["inbox-unread"], kept["inbox-open-task"], kept["inbox-done-task"])
	}
	if kept[fmt.Sprintf("inbox-read-%d", agentConversationPerUser)] {
		t.Fatal("capacity cleanup did not remove the oldest read conversation instead")
	}
}
