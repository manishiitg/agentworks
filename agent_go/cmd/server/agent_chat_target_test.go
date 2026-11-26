package server

import (
	"context"
	"testing"
)

// #4046: no implicit Main. A message with no agent goes to the caller's only agent; with several the caller is told to
// choose; "main" is not an alias.
func TestAgentChatTargetWithoutAgentUsesTheOnlyAgentNotMain(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	root := crewOwnerTabsFixture(t, env, "other")
	ctx := internalBotRequestContext(context.Background(), "other")
	// Each person has one agent here: the owner "boss", the co-owner "solo".
	for user, key := range map[string]string{"owner": "beta:chat:boss", "other": "beta:chat:solo"} {
		profile, err := env.api.agentProfiles.Resolve("work", 0, user)
		if err != nil {
			t.Fatal(err)
		}
		binding, _, err := resolveConversationBindingForUser(internalBotRequestContext(context.Background(), user), user, profile, key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := defaultProductConversationRegistryStore().resolveOrCreate(internalBotRequestContext(context.Background(), user), user, profile, binding, ""); err != nil {
			t.Fatal(err)
		}
	}
	target := triggerTarget{Kind: triggerCallerCrew, Path: root, CrewID: "beta", CrewProfile: "work", CrewOwner: "owner", Label: "Project"}
	only, err := env.api.resolveAgentChatTarget(ctx, target, "")
	if err != nil || only.Chat == nil || only.CrewOwner != "other" || only.Chat.Key != "beta:chat:solo" {
		t.Fatalf("no agent named: %+v %v", only, err)
	}
	owners, err := env.api.resolveAgentChatTarget(ctx, target, "beta:chat:boss")
	if err != nil || owners.Chat == nil || owners.CrewOwner != "owner" || owners.Chat.SessionID == only.Chat.SessionID {
		t.Fatalf("explicit owner tab: %+v %v", owners, err)
	}
	for _, chat := range []string{"unlisted", "main"} {
		if _, err := env.api.resolveAgentChatTarget(ctx, target, chat); err == nil {
			t.Fatalf("%q admitted", chat)
		}
	}
	// PLAT-746-477: a stored chat pointer cannot turn a workflow into a chat target.
	retired := triggerTarget{Kind: triggerCallerWorkflow, Chat: &codeChat{SessionID: "run-example"}}
	if _, err := env.api.resolveAgentChatTarget(ctx, retired, ""); err == nil {
		t.Fatal("workflow accepted a conversation message")
	}
	if err := validateMessageDestination(ctx, agentMessageEndpoint{Kind: triggerCallerWorkflow}); err == nil {
		t.Fatal("queued workflow conversation remained deliverable")
	}
	endpoint := targetMessageEndpoint("owner", owners)
	if endpoint.UserID != "owner" || endpoint.Session != owners.Chat.SessionID || endpoint.ChatKey != owners.Chat.Key {
		t.Fatal("recipient attribution lost", endpoint)
	}
}

func TestAgentChatTargetRunSharingDoesNotAdoptBuilderHistory(t *testing.T) {
	env := newTriggerLinkEnv(t)
	root := crewOwnerTabsFixture(t, env)
	ctx := internalBotRequestContext(context.Background(), "other")
	target := triggerTarget{Kind: triggerCallerCrew, Path: root, CrewID: "beta", CrewProfile: "work", CrewOwner: "owner", Label: "Project"}
	resolved, err := env.api.resolveAgentChatTarget(ctx, target, "")
	if err != nil || resolved.Chat != nil {
		t.Fatalf("Run caller adopted owner history: %+v %v", resolved, err)
	}
}
