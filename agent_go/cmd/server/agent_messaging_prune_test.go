package server

import (
	"context"
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

	// Stored: retention deletes the expired conversation's rows and its
	// attachment file, and keeps only the text-free expired metadata.
	env := newTriggerLinkEnv(t)
	ctx := context.Background()
	fileID := "file-0123456789abcdef0123456789abcdef"
	env.mock.mu.Lock()
	env.mock.files[agentMessageFilesDir+fileID+".json"] = `{"file_id":"` + fileID + `"}`
	env.mock.mu.Unlock()
	stored := agentMessageStore{Conversations: []agentConversation{conv("old-idle", "u1", 8*24*time.Hour, "delivered"), conv("fresh", "u1", time.Hour, "delivered")}}
	stored.Conversations[0].Messages[0].File = &agentMessageFileReference{ID: fileID, Name: "a.txt", Size: 1}
	if err := saveAgentMessageStore(ctx, stored); err != nil {
		t.Fatal(err)
	}
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	if err == nil {
		pruneAgentConversations(&s, now, "u1")
		err = saveAgentMessageStore(ctx, s)
	}
	agentMessageMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	mb, _ := currentAgentMailbox()
	var conversations, messages, expired int
	_ = mb.db.QueryRow(`SELECT (SELECT COUNT(*) FROM conversations WHERE inbox_id='old-idle'), (SELECT COUNT(*) FROM messages WHERE inbox_id='old-idle'), (SELECT COUNT(*) FROM expired_conversations WHERE inbox_id='old-idle')`).Scan(&conversations, &messages, &expired)
	if conversations != 0 || messages != 0 || expired != 1 {
		t.Fatalf("retention rows: conversations=%d messages=%d expired=%d", conversations, messages, expired)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		env.mock.mu.Lock()
		_, left := env.mock.files[agentMessageFilesDir+fileID+".json"]
		env.mock.mu.Unlock()
		if !left {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retention kept the removed message's file")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
