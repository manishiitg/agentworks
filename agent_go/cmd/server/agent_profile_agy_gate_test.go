package server

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestProfileOffersAgyOnLocalAndMultiUserServers(t *testing.T) {
	profile := agentprofiles.Profile{Runtime: agentprofiles.RuntimePolicy{ProviderOptions: []agentprofiles.ProviderOption{
		{ID: "codex-cli", Provider: "codex-cli"},
		{ID: "agy-cli", Provider: "agy-cli"},
	}}}
	t.Setenv("AGY_ALPHA", "")
	for _, mode := range []string{"", "true"} {
		t.Setenv("MULTI_USER_MODE", mode)
		if got := profileWithAvailableProviders(profile); len(got.Runtime.ProviderOptions) != 2 {
			t.Fatalf("provider option hidden in mode %q: %+v", mode, got.Runtime.ProviderOptions)
		}
	}
	if len(profile.Runtime.ProviderOptions) != 2 {
		t.Fatal("registry profile mutated")
	}
}
