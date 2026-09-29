package server

import (
	"context"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func codeProfileForDefaultTest() agentprofiles.Profile {
	var p agentprofiles.Profile
	p.Product = "code"
	p.Runtime.ProviderOptions = []agentprofiles.ProviderOption{
		{ID: "claude", Provider: "claude-code", ModelID: "claude-sonnet-5-5", Default: true},
		{ID: "cursor", Provider: "cursor-cli", ModelID: "auto"},
		{ID: "muse", Provider: "muse-cli", ModelID: "muse-1"},
	}
	return p
}

func defaultOf(p agentprofiles.Profile) string {
	for _, o := range p.Runtime.ProviderOptions {
		if o.Default {
			return o.Provider
		}
	}
	return ""
}

func TestDynamicProductDefaultPicksAProviderEveryoneCanUse(t *testing.T) {
	orig := providerReadyForEveryone
	t.Cleanup(func() { providerReadyForEveryone = orig })
	ready := map[string]bool{}
	providerReadyForEveryone = func(_ context.Context, provider, product string) bool { return product == "code" && ready[provider] }

	// Only Muse is signed in and open to everyone (Excellence): new Codes start on it.
	ready = map[string]bool{"muse-cli": true}
	p := codeProfileForDefaultTest()
	applyDynamicProductDefault(context.Background(), &p)
	if got := defaultOf(p); got != "muse-cli" {
		t.Fatalf("default = %s, want muse-cli", got)
	}
	// The profile's own default is kept when it works.
	ready = map[string]bool{"claude-code": true, "muse-cli": true}
	p = codeProfileForDefaultTest()
	applyDynamicProductDefault(context.Background(), &p)
	if got := defaultOf(p); got != "claude-code" {
		t.Fatalf("default = %s, want claude-code kept", got)
	}
	// Nothing ready: unchanged.
	ready = map[string]bool{}
	p = codeProfileForDefaultTest()
	applyDynamicProductDefault(context.Background(), &p)
	if got := defaultOf(p); got != "claude-code" {
		t.Fatalf("default = %s, want unchanged", got)
	}
}
