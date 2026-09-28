package server

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestProfileWithAvailableProvidersHidesAgyUnlessLocalAlpha(t *testing.T) {
	profile := agentprofiles.Profile{Runtime: agentprofiles.RuntimePolicy{ProviderOptions: []agentprofiles.ProviderOption{
		{ID: "codex-cli", Provider: "codex-cli"},
		{ID: "agy-cli", Provider: "agy-cli"},
	}}}
	t.Setenv("AGY_ALPHA", "")
	t.Setenv("MULTI_USER_MODE", "")
	if got := profileWithAvailableProviders(profile); len(got.Runtime.ProviderOptions) != 1 {
		t.Fatalf("AGY option exposed without alpha flag: %+v", got.Runtime.ProviderOptions)
	}
	t.Setenv("AGY_ALPHA", "1")
	if got := profileWithAvailableProviders(profile); len(got.Runtime.ProviderOptions) != 2 {
		t.Fatalf("AGY option missing in local alpha mode: %+v", got.Runtime.ProviderOptions)
	}
	t.Setenv("MULTI_USER_MODE", "true")
	if got := profileWithAvailableProviders(profile); len(got.Runtime.ProviderOptions) != 1 {
		t.Fatalf("AGY option exposed in multi-user mode: %+v", got.Runtime.ProviderOptions)
	}
	if len(profile.Runtime.ProviderOptions) != 2 {
		t.Fatal("filter mutated the registry profile")
	}
}
