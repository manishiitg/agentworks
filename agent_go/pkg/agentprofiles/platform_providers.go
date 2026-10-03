package agentprofiles

import (
	llm "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/utils"
	"strings"
)

// WithPlatformCodingProviders offers the platform registry and model catalog,
// retaining only this product's default overrides. Accounts remain platform-owned.
func WithPlatformCodingProviders(profile Profile) Profile {
	overrides := map[string]ProviderOption{}
	for _, option := range profile.Runtime.ProviderOptions {
		overrides[option.Provider] = option
	}
	var options []ProviderOption
	models := PlatformModelMetadata()
	for _, contract := range llm.CodingAgentProviderContracts() {
		if contract.Deprecated {
			continue
		}
		provider := string(contract.Provider)
		defaults, ok := llm.GetCodingAgentDefaultTierModels(contract.Provider)
		if !ok {
			continue
		}
		option, overridden := overrides[provider]
		if !overridden {
			option = ProviderOption{ID: provider, Provider: provider, Label: contract.DisplayName, ModelID: defaults.Builder.ModelID, Options: defaults.Builder.Options}
		}
		option.Models = nil
		fallbackEfforts := option.ReasoningEfforts
		option.ReasoningEfforts = nil
		seen := map[string]bool{}
		for _, model := range models {
			if model.Provider != provider {
				continue
			}
			for _, effort := range model.ReasoningEffortLevels {
				if !seen[effort] {
					seen[effort] = true
					option.ReasoningEfforts = append(option.ReasoningEfforts, effort)
				}
			}
		}
		if len(option.ReasoningEfforts) == 0 {
			option.ReasoningEfforts = fallbackEfforts
			for _, tier := range []llm.CodingAgentTierModelRef{defaults.Low, defaults.Medium, defaults.High, defaults.Builder, defaults.Pulse} {
				effort, _ := tier.Options["reasoning_effort"].(string)
				if effort != "" && !seen[effort] {
					seen[effort] = true
					found := false
					for _, existing := range option.ReasoningEfforts {
						if existing == effort {
							found = true
						}
					}
					if !found {
						option.ReasoningEfforts = append(option.ReasoningEfforts, effort)
					}
				}
			}
		}
		options = append(options, option)
	}
	profile.Runtime.ProviderOptions = options
	return profile
}

// PlatformModelMetadata is the shared catalog used by provider manifests and
// product runtime choices, including the native Claude CLI models.
func PlatformModelMetadata() []*llmtypes.ModelMetadata {
	var models []*llmtypes.ModelMetadata
	seen := map[string]bool{}
	for _, source := range [][]*llmtypes.ModelMetadata{utils.GetAllModelMetadata(), claudecode.GetAllClaudeCodeModels()} {
		for _, model := range source {
			if model == nil || strings.TrimSpace(model.Provider) == "" || strings.TrimSpace(model.ModelID) == "" {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(model.Provider)) + "\x00" + strings.TrimSpace(model.ModelID)
			if !seen[key] {
				seen[key] = true
				models = append(models, model)
			}
		}
	}
	return models
}
