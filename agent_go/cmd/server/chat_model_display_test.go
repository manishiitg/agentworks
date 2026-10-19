package server

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
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
