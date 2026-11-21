package server

import (
	"testing"
	"time"
)

func TestWorkflowBuilderOwnedConversationLogPathUsesPrivateUserFolder(t *testing.T) {
	got := workflowBuilderOwnedConversationLogPath("Workflow/instagram", "user-123", "abc-123", time.Date(2026, 4, 30, 10, 11, 12, 0, time.UTC))
	want := "Workflow/instagram/builder/conversation/users/user-123/2026-04-30/session-abc-123-conversation.json"
	if got != want {
		t.Fatalf("unexpected owned path:\n got: %s\nwant: %s", got, want)
	}
}

func TestEffectiveBuilderConversationOwnerNeverDowngradesExistingOwner(t *testing.T) {
	if got := effectiveBuilderConversationOwner("default", "real-user"); got != "real-user" {
		t.Fatalf("background owner downgrade: got %q", got)
	}
	if got := effectiveBuilderConversationOwner("request-user", "old-user"); got != "request-user" {
		t.Fatalf("request owner should win: got %q", got)
	}
}
