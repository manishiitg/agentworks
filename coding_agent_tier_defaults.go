package llmproviders

import "strings"

// CodingAgentTierModelRef is the provider-level default for a workflow tier.
type CodingAgentTierModelRef struct {
	Provider string                 `json:"provider"`
	ModelID  string                 `json:"model_id"`
	Options  map[string]interface{} `json:"options,omitempty"`
}

// CodingAgentDefaultTierModels describes a coding-agent provider's defaults:
// Main is the model a chat starts with, and High, Medium and Low are the
// workflow tiers (background work uses High).
type CodingAgentDefaultTierModels struct {
	Main   CodingAgentTierModelRef `json:"main"`
	High   CodingAgentTierModelRef `json:"high"`
	Medium CodingAgentTierModelRef `json:"medium"`
	Low    CodingAgentTierModelRef `json:"low"`
}

func codingAgentHighReasoningRef(provider, modelID string) CodingAgentTierModelRef {
	return codingAgentReasoningRef(provider, modelID, "high")
}

func codingAgentReasoningRef(provider, modelID, effort string) CodingAgentTierModelRef {
	return CodingAgentTierModelRef{
		Provider: provider,
		ModelID:  modelID,
		Options:  map[string]interface{}{"reasoning_effort": effort},
	}
}

// GetCodingAgentDefaultTierModels returns the provider-owned workflow tier
// defaults for coding-agent providers. Phase intentionally follows high.
//
// Update ritual when a coding-agent model changes:
//   - update the provider's GetAll*Models registry so the selector is visible
//     to UI/API callers;
//   - update these tier defaults;
//   - run TestCodingAgentDefaultTierModelsArePublished so stale hidden model
//     IDs fail before release.
func GetCodingAgentDefaultTierModels(provider Provider) (*CodingAgentDefaultTierModels, bool) {
	providerID := strings.TrimSpace(string(provider))

	switch Provider(providerID) {
	case ProviderCodexCLI:
		high := codingAgentReasoningRef(providerID, "gpt-6.1-terra", "medium")
		main := codingAgentReasoningRef(providerID, "gpt-6.2-sol", "high")
		medium := codingAgentReasoningRef(providerID, "gpt-6.1-terra", "high")
		low := codingAgentReasoningRef(providerID, "gpt-6-luna", "high")
		return &CodingAgentDefaultTierModels{
			Main:   main,
			High:   high,
			Medium: medium,
			Low:    low,
		}, true
	case ProviderClaudeCode:
		// Medium and Low run Haiku 5.5 (owner, 2026-10-16): high effort for
		// Medium, medium effort for Low; Main and High stay Sonnet.
		high := codingAgentHighReasoningRef(providerID, "claude-sonnet-5-5")
		medium := codingAgentReasoningRef(providerID, "claude-haiku-5-5", "high")
		main := codingAgentReasoningRef(providerID, "claude-sonnet-5-5", "medium")
		return &CodingAgentDefaultTierModels{
			Main:   main,
			High:   high,
			Medium: medium,
			Low:    codingAgentReasoningRef(providerID, "claude-haiku-5-5", "medium"),
		}, true
	case ProviderCursorCLI:
		// Use the same certified default as ordinary Cursor chats. Auto is
		// still selectable when a person's account offers it (PLAT-872).
		model := codingAgentHighReasoningRef(providerID, DefaultCursorCLIModel)
		return &CodingAgentDefaultTierModels{
			Main:   model,
			High:   model,
			Medium: model,
			Low:    model,
		}, true
	case ProviderPiCLI:
		// Gemini 4 Hydrogen for Main and High; 3.8 Flash and 3.5 Flash-Lite
		// stay on the cheaper tiers (owner, 2026-11-13).
		return &CodingAgentDefaultTierModels{
			Main:   codingAgentReasoningRef(providerID, "google/gemini-4-hydrogen", "high"),
			High:   codingAgentReasoningRef(providerID, "google/gemini-4-hydrogen", "high"),
			Medium: codingAgentReasoningRef(providerID, "google/gemini-3.8-flash", "medium"),
			Low:    codingAgentReasoningRef(providerID, "google/gemini-3.5-flash-lite", "low"),
		}, true
	case ProviderMuseCLI:
		// Spark 1.3 for Main, High and Medium; Low runs Muse Ember, the small
		// low-cost model (owner, 2026-11-10).
		const museModel = DefaultMuseCLIModel
		return &CodingAgentDefaultTierModels{
			Main:   codingAgentReasoningRef(providerID, museModel, "max"),
			High:   codingAgentReasoningRef(providerID, museModel, "xhigh"),
			Medium: codingAgentReasoningRef(providerID, museModel, "high"),
			Low:    codingAgentReasoningRef(providerID, "muse-ember-1.0", "high"),
		}, true
	case ProviderAgyCLI:
		// Effort is baked into agy's model slugs. Gemini 4 Hydrogen for Main
		// and High; 3.8 Flash for Medium and Low (owner, 2026-11-13).
		const agyModel = "gemini-3.8-flash"
		return &CodingAgentDefaultTierModels{
			Main:   codingAgentReasoningRef(providerID, "gemini-4-hydrogen-high", "high"),
			High:   codingAgentReasoningRef(providerID, "gemini-4-hydrogen-high", "high"),
			Medium: codingAgentReasoningRef(providerID, agyModel+"-medium", "medium"),
			Low:    codingAgentReasoningRef(providerID, agyModel+"-low", "low"),
		}, true
	}

	return nil, false
}
