package server

import (
	"context"
	"strings"
	"testing"
)

func TestAgentChatTargetDefaultsToCanonicalCrewMain(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	root := crewOwnerTabsFixture(t, env, "other")
	ctx := internalBotRequestContext(context.Background(), "other")
	profile, err := env.api.agentProfiles.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"owner", "other"} {
		if err := ensureCrewOwnerDefaultTab(ctx, user, profile, "beta"); err != nil {
			t.Fatal(err)
		}
	}
	target := triggerTarget{Kind: triggerCallerCrew, Path: root, CrewID: "beta", CrewProfile: "work", CrewOwner: "owner", Label: "Project"}
	main, err := env.api.resolveAgentChatTarget(ctx, target, "")
	if err != nil || main.Chat == nil || main.CrewOwner != "owner" {
		t.Fatalf("default main: %+v %v", main, err)
	}
	binding, _, err := resolveConversationBindingForUser(ctx, "owner", profile, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if main.Chat.Key != binding.ConversationKey || strings.HasPrefix(main.Chat.Key, "messages:") {
		t.Fatal("default created isolated message chat", main.Chat)
	}
	coowner, err := env.api.resolveAgentChatTarget(ctx, target, crewOwnerDefaultChatKey("beta", "other"))
	if err != nil || coowner.Chat == nil || coowner.CrewOwner != "other" || coowner.Chat.SessionID == main.Chat.SessionID {
		t.Fatalf("explicit coowner tab: %+v %v", coowner, err)
	}
	if _, err := env.api.resolveAgentChatTarget(ctx, target, "unlisted"); err == nil {
		t.Fatal("invented chat admitted")
	}
	endpoint := targetMessageEndpoint("other", main)
	if endpoint.UserID != "owner" || endpoint.Session != main.Chat.SessionID || endpoint.ChatKey != main.Chat.Key {
		t.Fatal("recipient attribution lost", endpoint)
	}
}
