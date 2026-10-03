package agentprofiles

import (
	llm "github.com/manishiitg/multi-llm-provider-go"
	"testing"
)

func TestPlatformCodingProvidersKeepRoleDefaultAndOfferRegistry(t *testing.T) {
	original := Profile{Runtime: RuntimePolicy{ProviderOptions: []ProviderOption{{ID: "codex-cli", Provider: "codex-cli", ModelID: "gpt-6.1-sol", Default: true, Models: []string{"gpt-6.1-sol"}, Options: map[string]interface{}{"reasoning_effort": "medium"}}}}}
	profile := WithPlatformCodingProviders(original)
	choices := map[string]ProviderOption{}
	for _, option := range profile.Runtime.ProviderOptions {
		choices[option.Provider] = option
		if len(option.Models) != 0 {
			t.Fatalf("%s narrows the platform catalog", option.Provider)
		}
	}
	for _, contract := range llm.CodingAgentProviderContracts() {
		if contract.Deprecated {
			continue
		}
		if _, ok := llm.GetCodingAgentDefaultTierModels(contract.Provider); !ok {
			continue
		}
		if _, ok := choices[string(contract.Provider)]; !ok {
			t.Fatalf("missing platform provider %s", contract.Provider)
		}
	}
	if choices["codex-cli"].ModelID != "gpt-6.1-sol" || !choices["codex-cli"].Default {
		t.Fatal("parent default changed")
	}
	if len(original.Runtime.ProviderOptions[0].Models) != 1 {
		t.Fatal("modified original profile")
	}
}
