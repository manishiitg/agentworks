package mcpagent

import (
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestCLISecurityPolicyUsesTrustedProviderAndCopiesState(t *testing.T) {
	input := llmtypes.CLISecurityPolicy{
		Mode:                llmtypes.CLISecurityModeVerified,
		Provider:            "model-supplied-provider",
		WorkspaceWritePaths: []string{"Workflow/demo"},
	}
	agent := &Agent{}
	withCLISecurityPolicy(input)(agent)
	input.WorkspaceWritePaths[0] = "mutated"

	options := agent.appendCLISecurityPolicyOption(nil, llm.ProviderCodexCLI)
	if len(options) != 1 {
		t.Fatalf("options = %d, want 1", len(options))
	}
	var resolved llmtypes.CallOptions
	options[0](&resolved)
	if resolved.CLISecurity == nil {
		t.Fatal("missing CLI security policy")
	}
	if got := resolved.CLISecurity.Provider; got != "codex-cli" {
		t.Fatalf("provider = %q, want codex-cli", got)
	}
	if got := resolved.CLISecurity.WorkspaceWritePaths[0]; got != "Workflow/demo" {
		t.Fatalf("policy aliased caller state: %q", got)
	}
}
