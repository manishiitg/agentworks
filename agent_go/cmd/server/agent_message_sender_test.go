package server

import (
	"context"
	"testing"
)

// A Watcher's event reads as a Watcher in the chat, never as the Crew's own default agent.
func TestAgentMessageSenderNamesWatcherNotDefaultAgent(t *testing.T) {
	source := agentMessageEndpoint{UserID: "u", Kind: triggerCallerCrew, ID: "crew", ConditionJob: "job", Label: "Watcher: Notion tickets"}
	label, agent, _ := agentMessageSender(context.Background(), source, "u")
	if label != "Watcher: Notion tickets" || agent != "Watcher" {
		t.Fatalf("label=%q agent=%q", label, agent)
	}
	if icon := agentMessageAvatarFor(source).AvatarIcon; icon != "eye" {
		t.Fatalf("avatar icon %q", icon)
	}
}
