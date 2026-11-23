package llmproviders

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestCLISecurityPolicyDefaultsToCompatibilityAndClones(t *testing.T) {
	input := llmtypes.CLISecurityPolicy{
		Provider:            " CODEX-CLI ",
		WorkspaceWritePaths: []string{"Workflow/demo"},
	}
	var opts llmtypes.CallOptions
	WithCLISecurityPolicy(input)(&opts)
	if opts.CLISecurity == nil {
		t.Fatal("expected CLI security policy")
	}
	if got := opts.CLISecurity.Mode; got != llmtypes.CLISecurityModeCompatibility {
		t.Fatalf("mode = %q, want compatibility", got)
	}
	if got := opts.CLISecurity.Provider; got != "codex-cli" {
		t.Fatalf("provider = %q, want codex-cli", got)
	}
	input.WorkspaceWritePaths[0] = "mutated"
	if got := opts.CLISecurity.WorkspaceWritePaths[0]; got != "Workflow/demo" {
		t.Fatalf("policy aliased caller slice: %q", got)
	}
}

func TestCodingAgentSecurityProfileReturnsDeepCopy(t *testing.T) {
	first, ok := GetCodingAgentSecurityProfile(ProviderCodexCLI)
	if !ok {
		t.Fatal("missing Codex security profile")
	}
	if !first.Certified {
		t.Fatal("Codex profile should be certified after sandbox and installed-CLI E2E coverage")
	}
	first.Executables[0] = "mutated"
	first.Capabilities[0].ReadPathTemplates[0] = "mutated"

	second, _ := GetCodingAgentSecurityProfile(ProviderCodexCLI)
	if second.Executables[0] != "codex" {
		t.Fatalf("executable profile was mutated: %q", second.Executables[0])
	}
	if second.Capabilities[0].ReadPathTemplates[0] != "~/.codex" {
		t.Fatalf("capability profile was mutated: %q", second.Capabilities[0].ReadPathTemplates[0])
	}
}
