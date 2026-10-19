package server

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

func TestChatModelDisplayUsesAgentAndCatalogNames(t *testing.T) {
	runtime := agentprofiles.RuntimePolicy{ProviderOptions: []agentprofiles.ProviderOption{
		{ID: "cursor", Provider: "cursor-cli", ModelID: "auto"},
		{ID: "claude", Provider: "claude-code", ModelID: "claude-sonnet-5-5"},
	}}
	for _, check := range []struct{ engine, model, want string }{
		{"cursor", "auto", "Cursor · auto"},
		{"cursor", "", "Cursor · auto"},
		{"claude", "claude-sonnet-5-5", "Claude · Sonnet 5.5"},
		{"claude-code", "", "Claude · Sonnet 5.5"},
		{"pi-cli", "custom/private-model", "Pi · custom/private-model"},
	} {
		if got := chatModelDisplayLabel(runtime, check.engine, check.model); got != check.want {
			t.Errorf("label(%q, %q) = %q; want %q", check.engine, check.model, got, check.want)
		}
	}
	if runtime.ProviderOptions[1].ModelID != "claude-sonnet-5-5" {
		t.Fatal("display formatting changed the runtime model ID")
	}
}

func TestEffectiveChatModelIsResolvedByServerFromCurrentProjectAndOwnChoice(t *testing.T) {
	profile := engineModelTestProfile()
	profile.Runtime.ProviderOptions[1].ReasoningEfforts = []string{"high"}
	binding := productConversationBinding{ProjectLLMConfig: &workflowtypes.PresetLLMConfig{
		Mode: "explicit", BuilderLLM: &workflowtypes.AgentLLMConfig{Provider: "claude-code", ModelID: "claude-sonnet-5-5", ConnectionID: "project-account"},
	}}
	chat := ProductConversationRecord{ConversationKey: "crew:chat:review", SessionID: "session", WorkspacePath: "Chats/Crew", Provider: "muse-cli", ModelID: "old-model", ConnectionID: "old-account"}
	inherited, err := effectiveProfileChatModel(profile, binding, chat, ProductAgentIdentity{})
	if err != nil || inherited.OwnModel || inherited.Engine != "claude-code" || inherited.ModelID != "claude-sonnet-5-5" || inherited.ConnectionID != "project-account" {
		t.Fatalf("project model = %+v, %v", inherited, err)
	}
	otherProvider, err := effectiveProfileChatModel(profile, binding, chat, ProductAgentIdentity{Engine: "codex-cli", ModelID: "gpt-6-astra", ReasoningEffort: "high"})
	if err != nil || !otherProvider.OwnModel || otherProvider.Engine != "codex-cli" || otherProvider.ModelID != "gpt-6-astra" || otherProvider.ConnectionID != "" || otherProvider.ReasoningEffort != "high" {
		t.Fatalf("own model = %+v, %v", otherProvider, err)
	}
	sameProvider, err := effectiveProfileChatModel(profile, binding, chat, ProductAgentIdentity{Engine: "claude-code", ModelID: "claude-opus-5-5"})
	if err != nil || sameProvider.ConnectionID != "project-account" || sameProvider.ModelID != "claude-opus-5-5" {
		t.Fatalf("same-provider choice = %+v, %v", sameProvider, err)
	}
	noConfig, err := effectiveProfileChatModel(profile, productConversationBinding{}, chat, ProductAgentIdentity{})
	if err != nil || noConfig.ModelID != "claude-fable-5-1" || noConfig.ConnectionID != "" {
		t.Fatalf("missing project config inferred old runtime: %+v, %v", noConfig, err)
	}
	if _, err := effectiveProfileChatModel(profile, binding, chat, ProductAgentIdentity{Engine: "foreign-engine", ModelID: "foreign-model"}); err == nil {
		t.Fatal("accepted an unavailable engine")
	}
	if chat.Provider != "muse-cli" || chat.ModelID != "old-model" || chat.ConnectionID != "old-account" {
		t.Fatal("metadata resolution mutated the stored conversation")
	}
	chat.Provider, chat.ModelID = "claude-code", "claude-opus-5-5"
	currentAccount, err := effectiveProfileChatModel(profile, productConversationBinding{}, chat, ProductAgentIdentity{})
	if err != nil || currentAccount.ModelID != "claude-fable-5-1" || currentAccount.ConnectionID != "old-account" {
		t.Fatalf("default model inferred old model or lost current same-provider account: %+v, %v", currentAccount, err)
	}
}
