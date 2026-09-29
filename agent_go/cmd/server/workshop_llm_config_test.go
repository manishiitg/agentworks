package server

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

func TestWorkshopConvertTieredLLMConfigHandlesPartialTiers(t *testing.T) {
	tiered := workshopConvertTieredLLMConfig(&workflowtypes.TieredLLMConfig{
		Tier2: &workflowtypes.AgentLLMConfig{
			Provider: "kimi",
			ModelID:  "kimi-k2.6",
		},
	})

	if tiered == nil {
		t.Fatal("expected non-nil tiered config")
	}
	if tiered.Tier1 != nil {
		t.Fatalf("expected nil tier1, got %+v", tiered.Tier1)
	}
	if tiered.Tier2 == nil {
		t.Fatal("expected non-nil tier2")
	}
	if tiered.Tier2.Provider != "kimi" || tiered.Tier2.ModelID != "kimi-k2.6" {
		t.Fatalf("unexpected tier2 config: %+v", tiered.Tier2)
	}
	if tiered.Tier3 != nil {
		t.Fatalf("expected nil tier3, got %+v", tiered.Tier3)
	}
}

func TestWorkshopConvertAgentLLMConfigPreservesPublishedOptions(t *testing.T) {
	converted := workshopConvertAgentLLMConfig(&workflowtypes.AgentLLMConfig{
		PublishedLLMID: "claude-low",
		Provider:       "claude-code",
		ModelID:        "sonnet",
		Options: map[string]interface{}{
			"reasoning_effort": "low",
		},
	})

	if converted == nil {
		t.Fatal("expected converted config")
	}
	if converted.PublishedLLMID != "claude-low" {
		t.Fatalf("expected published id to be preserved, got %q", converted.PublishedLLMID)
	}
	if got := converted.Options["reasoning_effort"]; got != "low" {
		t.Fatalf("expected primary reasoning_effort=low, got %v", got)
	}

}

func TestWorkshopResolveLLMConfigExpandsCodingAgentMode(t *testing.T) {
	defaults, ok := llmproviders.GetCodingAgentDefaultTierModels(llmproviders.ProviderClaudeCode)
	if !ok {
		t.Fatal("expected Claude Code coding-agent defaults")
	}
	// multi-llm-provider-go coding_agent_tier_defaults.go (2026-09-23):
	// Opus 5.5 builds; Sonnet 5.5 backs High, Medium, Low and Pulse (= High).
	if defaults.Builder.ModelID != "claude-opus-5-5" ||
		defaults.High.ModelID != "claude-sonnet-5-5" ||
		defaults.Medium.ModelID != "claude-sonnet-5-5" ||
		defaults.Low.ModelID != "claude-sonnet-5-5" ||
		defaults.Pulse.ModelID != defaults.High.ModelID {
		t.Fatalf("Opus 5.5 should back Claude Builder and Sonnet 5.5 High/Medium/Low/Pulse, got defaults: %+v", defaults)
	}

	builder, tiered := workshopResolveLLMConfig(&workflowtypes.PresetLLMConfig{
		SchemaVersion: workflowtypes.LLMConfigSchemaVersion,
		Mode:          workflowtypes.LLMConfigModeProviderProfile,
		Provider:      "claude-code",
	})

	if builder == nil {
		t.Fatal("expected provider profile builder LLM")
	}
	if builder.Provider != defaults.Builder.Provider || builder.ModelID != defaults.Builder.ModelID {
		t.Fatalf("unexpected builder config: %+v", builder)
	}
	if got := builder.Options["reasoning_effort"]; got != defaults.Builder.Options["reasoning_effort"] {
		t.Fatalf("builder reasoning_effort = %#v, want %#v", got, defaults.Builder.Options["reasoning_effort"])
	}
	pulse := workshopResolvePulseLLMConfig(&workflowtypes.PresetLLMConfig{
		SchemaVersion: workflowtypes.LLMConfigSchemaVersion,
		Mode:          workflowtypes.LLMConfigModeProviderProfile,
		Provider:      "claude-code",
	})
	if pulse == nil {
		t.Fatal("expected coding agent Pulse LLM")
	}
	if pulse.Provider != defaults.Pulse.Provider || pulse.ModelID != defaults.Pulse.ModelID {
		t.Fatalf("unexpected Pulse config: %+v", pulse)
	}
	if tiered == nil || tiered.Tier1 == nil || tiered.Tier2 == nil || tiered.Tier3 == nil {
		t.Fatalf("expected full tiered config, got %+v", tiered)
	}
	if tiered.Tier1.Provider != defaults.High.Provider || tiered.Tier1.ModelID != defaults.High.ModelID {
		t.Fatalf("unexpected high tier: %+v", tiered.Tier1)
	}
	if got := tiered.Tier1.Options["reasoning_effort"]; got != "high" {
		t.Fatalf("high reasoning_effort = %#v, want high", got)
	}
	if tiered.Tier2.Provider != defaults.Medium.Provider || tiered.Tier2.ModelID != defaults.Medium.ModelID {
		t.Fatalf("unexpected medium tier: %+v", tiered.Tier2)
	}
	if got := tiered.Tier2.Options["reasoning_effort"]; got != "medium" {
		t.Fatalf("medium reasoning_effort = %#v, want medium", got)
	}
	if tiered.Tier3.Provider != defaults.Low.Provider || tiered.Tier3.ModelID != defaults.Low.ModelID {
		t.Fatalf("unexpected low tier: %+v", tiered.Tier3)
	}
}

func TestGenerateTextWorkflowTiersExpandsCursorProviderProfile(t *testing.T) {
	defaults, ok := llmproviders.GetCodingAgentDefaultTierModels(llmproviders.ProviderCursorCLI)
	if !ok {
		t.Fatal("expected Cursor coding-agent defaults")
	}

	tiers := generateTextWorkflowTiers(&workflowtypes.PresetLLMConfig{
		SchemaVersion: workflowtypes.LLMConfigSchemaVersion,
		Mode:          workflowtypes.LLMConfigModeProviderProfile,
		Provider:      string(llmproviders.ProviderCursorCLI),
	})
	if tiers == nil || tiers.High == nil || tiers.Medium == nil || tiers.Low == nil {
		t.Fatalf("generateTextWorkflowTiers() = %+v, want all Cursor workflow tiers", tiers)
	}
	if tiers.High.Provider != defaults.High.Provider || tiers.High.ModelID != defaults.High.ModelID {
		t.Fatalf("high tier = %+v, want Cursor workflow high %+v", tiers.High, defaults.High)
	}
	if tiers.Medium.Provider != defaults.Medium.Provider || tiers.Medium.ModelID != defaults.Medium.ModelID {
		t.Fatalf("medium tier = %+v, want Cursor workflow medium %+v", tiers.Medium, defaults.Medium)
	}
	if tiers.Low.Provider != defaults.Low.Provider || tiers.Low.ModelID != defaults.Low.ModelID {
		t.Fatalf("low tier = %+v, want Cursor workflow low %+v", tiers.Low, defaults.Low)
	}
}
