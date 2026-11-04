package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// The restart sweep starts a delivery worker only for a pending internal
// message; idle history and external inboxes (even a malformed old pending
// external message) never start an internal turn.
func TestAgentMessageResumeSkipsIdleHistory(t *testing.T) {
	env := newTriggerLinkEnv(t)
	s := agentMessageStore{}
	for i := 0; i < 314; i++ {
		status := "delivered"
		if i%2 == 0 {
			status = "stored"
		}
		s.Conversations = append(s.Conversations, agentConversation{ID: fmt.Sprintf("idle-%d", i), Endpoints: [2]agentMessageEndpoint{{External: true}, {External: true}}, Messages: []agentMessage{{From: 0, Delivery: status}}})
	}
	s.Conversations[0].Messages[0].Delivery = "pending"
	if err := saveAgentMessageStore(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	for _, c := range s.Conversations {
		if pendingInternalAgentMessage(c) {
			t.Fatalf("%s counted as pending internal delivery", c.ID)
		}
	}
	env.api.resumeAgentMessages()
	agentMessageWorkers.Range(func(key, _ any) bool {
		if strings.HasPrefix(key.(string), "idle-") {
			t.Errorf("idle sweep started worker %s", key)
		}
		return true
	})
}
