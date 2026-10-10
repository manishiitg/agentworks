package server

import (
	"fmt"
	"testing"
	"time"
)

// The shared message store must never fill up for good: idle conversations
// expire, a busy sender cannot take every slot, and a waiting message keeps
// its conversation.
func TestAgentConversationsArePrunedSoMessagingNeverFillsUp(t *testing.T) {
	now := time.Now().UTC()
	conv := func(id, user string, age time.Duration, delivery string) agentConversation {
		return agentConversation{ID: id, Endpoints: [2]agentMessageEndpoint{{UserID: user}, {UserID: "owner"}},
			Messages: []agentMessage{{SentAt: now.Add(-age), Delivery: delivery}}}
	}
	s := agentMessageStore{Conversations: []agentConversation{
		conv("old-idle", "u1", 8*24*time.Hour, "delivered"),
		conv("old-waiting", "u1", 8*24*time.Hour, "pending"),
		conv("fresh", "u1", time.Hour, "delivered"),
	}}
	pruneAgentConversations(&s, now, "u1")
	if len(s.Conversations) != 2 || s.Conversations[0].ID != "old-waiting" || s.Conversations[1].ID != "fresh" {
		t.Fatalf("expected the waiting and fresh conversations to stay, got %+v", s.Conversations)
	}
	s = agentMessageStore{}
	for i := 0; i < agentConversationPerUser; i++ {
		s.Conversations = append(s.Conversations, conv(fmt.Sprintf("c%d", i), "busy", time.Duration(i+1)*time.Minute, "delivered"))
	}
	s.Conversations = append(s.Conversations, conv("other", "quiet", time.Hour, "delivered"))
	pruneAgentConversations(&s, now, "busy")
	if len(s.Conversations) != agentConversationPerUser {
		t.Fatalf("a sender at its cap should lose its own oldest conversation, got %d", len(s.Conversations))
	}
	for _, c := range s.Conversations {
		if c.ID == fmt.Sprintf("c%d", agentConversationPerUser-1) {
			t.Fatal("the oldest conversation of the busy sender should have been dropped")
		}
	}
	found := false
	for _, c := range s.Conversations {
		found = found || c.ID == "other"
	}
	if !found {
		t.Fatal("another user's conversation must not be dropped")
	}
}
