package server

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/mcpagent/llm"
)

// TestClaudeCodeTokenMissingForSingleProductDeploymentGatesCorrectly pins the
// exact incident this gate closes: a dedicated single-product deployment
// (Dominion, Video Studio, Finance) resolving to claude-code with no
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
			agentProducts:   "dominion",
			resolvedProfile: &resolvedAgentProfile{APIKeys: nil},
			finalProvider:   "claude-code",
			wantMissing:     true,
		},
		{
			name:            "single-product deployment, claude-code, keys present but token nil",
			agentProducts:   "dominion",
			resolvedProfile: &resolvedAgentProfile{APIKeys: &llm.ProviderAPIKeys{}},
			finalProvider:   "claude-code",
			wantMissing:     true,
		},
		{
			name:            "single-product deployment, claude-code, token blank",
			agentProducts:   "dominion",
			resolvedProfile: &resolvedAgentProfile{APIKeys: &llm.ProviderAPIKeys{ClaudeCodeOAuthToken: &blank}},
			finalProvider:   "claude-code",
			wantMissing:     true,
		},
		{
			name:            "single-product deployment, claude-code, token present",
			agentProducts:   "dominion",
			resolvedProfile: &resolvedAgentProfile{APIKeys: &llm.ProviderAPIKeys{ClaudeCodeOAuthToken: &token}},
			finalProvider:   "claude-code",
			wantMissing:     false,
		},
		{
			name:            "single-product deployment, non-claude-code provider, no token",
			agentProducts:   "dominion",
			resolvedProfile: &resolvedAgentProfile{APIKeys: nil},
			finalProvider:   "openai",
			wantMissing:     false,
		},
		{
			name:            "no active product profile (profile-less chat)",
			agentProducts:   "dominion",
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
			agentProducts:   "video-studio,dominion",
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
			name:          "profile does not opt in and server is shared/desktop -- stays exempt (e.g. Video Studio)",
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

// Code is private to each person and uses their own coding CLI login: on a multi-user server it must keep
// refusing a claude-code turn that has no token even when the server offers several products (Code next to
// Crew), because the fallback would be the platform account's own CLI login.
func TestClaudeCodeTokenMissingKeepsCodePrivateOnAMultiProductMultiUserServer(t *testing.T) {
	t.Setenv("AGENT_PRODUCTS", "code,work")
	t.Setenv("MULTI_USER_MODE", "true")
	code := &resolvedAgentProfile{APIKeys: nil}
	code.Definition.ID = "code"
	if !claudeCodeTokenMissingForSingleProductDeployment(code, "claude-code") {
		t.Fatal("a Code turn with no personal token must be refused on a multi-user multi-product server")
	}
	token := "sk-ant-oat01-test"
	code.APIKeys = &llm.ProviderAPIKeys{ClaudeCodeOAuthToken: &token}
	if claudeCodeTokenMissingForSingleProductDeployment(code, "claude-code") {
		t.Fatal("a Code turn with the user's own token must go through")
	}
	// another product on the same server keeps the shared-server behavior (ambient login allowed)
	crew := &resolvedAgentProfile{APIKeys: nil}
	crew.Definition.ID = "work"
	if claudeCodeTokenMissingForSingleProductDeployment(crew, "claude-code") {
		t.Fatal("the rule must not extend to other products")
	}
	// a single-user install (desktop) is unchanged
	t.Setenv("MULTI_USER_MODE", "")
	code.APIKeys = nil
	if claudeCodeTokenMissingForSingleProductDeployment(code, "claude-code") {
		t.Fatal("a single-user install must keep using the local CLI login")
	}
}
