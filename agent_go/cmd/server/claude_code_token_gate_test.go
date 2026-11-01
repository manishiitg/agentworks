package server

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/mcpagent/llm"
)

// TestClaudeCodeTokenMissingForSingleProductDeploymentGatesCorrectly pins the
// exact incident this gate closes: a dedicated single-product deployment
// (ExampleApp or Example Product) resolving to claude-code with no
// configured token must refuse instead of silently falling back to whatever
// Claude Code CLI login happens to exist on that host.
func TestClaudeCodeTokenMissingForSingleProductDeploymentGatesCorrectly(t *testing.T) {
	token := "sk-ant-oat01-test-token"
	blank := "   "

	cases := []struct {
		name            string
		agentProducts   string
		resolvedProfile *resolvedAgentProfile
		finalProvider   string
		wantMissing     bool
	}{
		{
			name:            "single-product deployment, claude-code, no profile keys at all",
			agentProducts:   "example-app",
			resolvedProfile: &resolvedAgentProfile{APIKeys: nil},
			finalProvider:   "claude-code",
			wantMissing:     true,
		},
		{
			name:            "single-product deployment, claude-code, keys present but token nil",
			agentProducts:   "example-app",
			resolvedProfile: &resolvedAgentProfile{APIKeys: &llm.ProviderAPIKeys{}},
			finalProvider:   "claude-code",
			wantMissing:     true,
		},
		{
			name:            "single-product deployment, claude-code, token blank",
			agentProducts:   "example-app",
			resolvedProfile: &resolvedAgentProfile{APIKeys: &llm.ProviderAPIKeys{ClaudeCodeOAuthToken: &blank}},
			finalProvider:   "claude-code",
			wantMissing:     true,
		},
		{
			name:            "single-product deployment, claude-code, token present",
			agentProducts:   "example-app",
			resolvedProfile: &resolvedAgentProfile{APIKeys: &llm.ProviderAPIKeys{ClaudeCodeOAuthToken: &token}},
			finalProvider:   "claude-code",
			wantMissing:     false,
		},
		{
			name:            "single-product deployment, non-claude-code provider, no token",
			agentProducts:   "example-app",
			resolvedProfile: &resolvedAgentProfile{APIKeys: nil},
			finalProvider:   "openai",
			wantMissing:     false,
		},
		{
			name:            "no active product profile (profile-less chat)",
			agentProducts:   "example-app",
			resolvedProfile: nil,
			finalProvider:   "claude-code",
			wantMissing:     false,
		},
		{
			name:            "shared desktop/multi-product server (AGENT_PRODUCTS unset) must stay exempt",
			agentProducts:   "",
			resolvedProfile: &resolvedAgentProfile{APIKeys: nil},
			finalProvider:   "claude-code",
			wantMissing:     false,
		},
		{
			name:            "server hosting more than one product is not a dedicated single-product deployment",
			agentProducts:   "example-product,example-app",
			resolvedProfile: &resolvedAgentProfile{APIKeys: nil},
			finalProvider:   "claude-code",
			wantMissing:     false,
		},
		{
			name:          "profile opts in via require_provider_token even on a shared/desktop server",
			agentProducts: "",
			resolvedProfile: &resolvedAgentProfile{
				Definition: agentprofiles.Profile{Runtime: agentprofiles.RuntimePolicy{RequireProviderToken: true}},
				APIKeys:    nil,
			},
			finalProvider: "claude-code",
			wantMissing:   true,
		},
		{
			name:          "profile opts in via require_provider_token but a token is present",
			agentProducts: "",
			resolvedProfile: &resolvedAgentProfile{
				Definition: agentprofiles.Profile{Runtime: agentprofiles.RuntimePolicy{RequireProviderToken: true}},
				APIKeys:    &llm.ProviderAPIKeys{ClaudeCodeOAuthToken: &token},
			},
			finalProvider: "claude-code",
			wantMissing:   false,
		},
		{
			name:          "profile does not opt in and server is shared/desktop -- stays exempt (e.g. Example Product)",
			agentProducts: "",
			resolvedProfile: &resolvedAgentProfile{
				Definition: agentprofiles.Profile{Runtime: agentprofiles.RuntimePolicy{RequireProviderToken: false}},
				APIKeys:    nil,
			},
			finalProvider: "claude-code",
			wantMissing:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AGENT_PRODUCTS", tc.agentProducts)
			if got := claudeCodeTokenMissingForSingleProductDeployment(tc.resolvedProfile, tc.finalProvider); got != tc.wantMissing {
				t.Fatalf("claudeCodeTokenMissingForSingleProductDeployment() = %v, want %v", got, tc.wantMissing)
			}
		})
	}
}

// Code and Crew treat the shared Claude login alike on a multi-product multi-user server: a Code turn with no
// personal token goes through, as a Crew turn does.
func TestClaudeCodeUsesTheSharedLoginLikeCrewOnAMultiProductMultiUserServer(t *testing.T) {
	t.Setenv("AGENT_PRODUCTS", "code,work")
	t.Setenv("MULTI_USER_MODE", "true")
	for _, id := range []string{"code", "work"} {
		p := &resolvedAgentProfile{APIKeys: nil}
		p.Definition.ID = id
		if claudeCodeTokenMissingForSingleProductDeployment(p, "claude-code") {
			t.Fatalf("a %s turn with no personal token must go through on a multi-product server", id)
		}
	}
}
