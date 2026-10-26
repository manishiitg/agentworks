package workflowtypes

import (
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"reflect"
	"testing"
)

func testExplicitConfig() *PresetLLMConfig {
	builder := &AgentLLMConfig{Provider: "claude-code", ModelID: "claude-sonnet-5-5"}
	high := &AgentLLMConfig{Provider: "claude-code", ModelID: "claude-opus-4-8"}
	return &PresetLLMConfig{
		SchemaVersion: LLMConfigSchemaVersion,
		Mode:          LLMConfigModeExplicit,
		BuilderLLM:    builder,
		BackgroundLLM: builder,
		TieredConfig: &TieredLLMConfig{
			Tier1: high,
			Tier2: builder,
			Tier3: &AgentLLMConfig{Provider: "claude-code", ModelID: "claude-haiku-4-5-20251001"},
		},
	}
}

func TestValidatePresetLLMConfigExplicit(t *testing.T) {
	cfg := testExplicitConfig()
	if err := ValidatePresetLLMConfigPublic(cfg); err != nil {
		t.Fatalf("ValidatePresetLLMConfigPublic() error = %v", err)
	}
}

func TestValidatePresetLLMConfigRequiresBuilder(t *testing.T) {
	cfg := testExplicitConfig()
	cfg.BuilderLLM = nil
	if err := ValidatePresetLLMConfigPublic(cfg); err == nil {
		t.Fatal("ValidatePresetLLMConfigPublic() error = nil, want missing builder error")
	}
}

func TestNormalizePresetLLMConfigMigratesLegacyExplicitShape(t *testing.T) {
	legacyBuilder := &AgentLLMConfig{Provider: "claude-code", ModelID: "claude-sonnet-5-5"}
	legacyMaintenance := &AgentLLMConfig{Provider: "claude-code", ModelID: "claude-opus-4-8"}
	cfg := &PresetLLMConfig{
		Provider:                "claude-code",
		LegacyModelID:           "claude-sonnet-5-5",
		LegacyPhaseLLM:          legacyBuilder,
		LegacyAutoImproveLLM:    legacyMaintenance,
		LegacyLLMAllocationMode: "tiered",
		TieredConfig:            &TieredLLMConfig{Tier1: legacyMaintenance, Tier2: legacyBuilder, Tier3: legacyBuilder},
	}

	if !NormalizePresetLLMConfig(cfg) {
		t.Fatal("NormalizePresetLLMConfig() changed = false")
	}
	if cfg.SchemaVersion != LLMConfigSchemaVersion || cfg.Mode != LLMConfigModeExplicit {
		t.Fatalf("normalized schema/mode = %d/%q", cfg.SchemaVersion, cfg.Mode)
	}
	if cfg.BuilderLLM != legacyBuilder || cfg.BackgroundLLM != legacyMaintenance {
		t.Fatalf("normalized roles = builder:%+v pulse:%+v", cfg.BuilderLLM, cfg.BackgroundLLM)
	}
	if cfg.Provider != "" || cfg.LegacyPhaseLLM != nil || cfg.LegacyAutoImproveLLM != nil || cfg.LegacyLLMAllocationMode != "" {
		t.Fatalf("legacy fields not cleared: %+v", cfg)
	}
}

func providerProfile(provider string) *PresetLLMConfig {
	return &PresetLLMConfig{
		SchemaVersion: LLMConfigSchemaVersion,
		Mode:          LLMConfigModeProviderProfile,
		Provider:      provider,
	}
}

func TestNormalizePresetLLMConfigMigratesLegacyCodingAgentToProviderProfile(t *testing.T) {
	cfg := &PresetLLMConfig{
		Provider:                "claude-code",
		LegacyModelID:           "claude-code",
		LegacyLLMAllocationMode: "coding_agent",
	}
	if !NormalizePresetLLMConfig(cfg) {
		t.Fatal("NormalizePresetLLMConfig() changed = false")
	}
	if cfg.Mode != LLMConfigModeProviderProfile || cfg.Provider != "claude-code" {
		t.Fatalf("normalized profile = mode:%q provider:%q", cfg.Mode, cfg.Provider)
	}
	if cfg.BuilderLLM != nil || cfg.TieredConfig != nil || cfg.LegacyModelID != "" {
		t.Fatalf("provider profile retained explicit/legacy fields: %+v", cfg)
	}
}

func TestNormalizePresetLLMConfigRemovesRetiredWorkflowKBLock(t *testing.T) {
	locked := true
	cfg := testExplicitConfig()
	cfg.LegacyLockKnowledgebase = &locked

	if !NormalizePresetLLMConfig(cfg) {
		t.Fatal("NormalizePresetLLMConfig() changed = false")
	}
	if cfg.LegacyLockKnowledgebase != nil {
		t.Fatalf("retired lock_knowledgebase survived normalization: %+v", cfg)
	}
}

// PLAT-872: workflow profiles must take current provider defaults and retain
// the selected account across every tier. Frozen model-name assertions here
// had become stale while the runtime correctly followed the provider registry.
func TestResolveProviderProfileConfigUsesBuilderDefaults(t *testing.T) {
	for _, provider := range []string{"claude-code", "codex-cli", "cursor-cli"} {
		t.Run(provider, func(t *testing.T) {
			expected, ok := llmproviders.GetCodingAgentDefaultTierModels(llmproviders.Provider(provider))
			if !ok {
				t.Fatal("provider defaults unavailable")
			}
			config := providerProfile(provider)
			config.ConnectionID = "selected-test-account"
			builder, tiers, ok := ResolveProviderProfileConfig(config)
			if !ok || builder == nil || tiers == nil {
				t.Fatal("workflow profile did not resolve all roles")
			}
			for _, pair := range []struct {
				got  *AgentLLMConfig
				want llmproviders.CodingAgentTierModelRef
			}{
				{builder, expected.Builder}, {tiers.Tier1, expected.High}, {tiers.Tier2, expected.Medium}, {tiers.Tier3, expected.Low},
			} {
				if pair.got == nil || pair.got.Provider != pair.want.Provider || pair.got.ModelID != pair.want.ModelID || !reflect.DeepEqual(pair.got.Options, pair.want.Options) || pair.got.ConnectionID != config.ConnectionID {
					t.Fatalf("workflow role %+v does not match provider default %+v and selected account", pair.got, pair.want)
				}
			}
		})
	}
}

func TestResolveProviderProfileBackgroundConfigUsesProviderDefault(t *testing.T) {
	got, ok := ResolveProviderProfileBackgroundConfig(providerProfile("claude-code"))
	if !ok || got.Provider != "claude-code" || got.ModelID != "claude-sonnet-5-5" || got.Options["reasoning_effort"] != "high" {
		t.Fatalf("ResolveProviderProfileBackgroundConfig() = %+v, %v", got, ok)
	}
}

func TestResolveCodingAgentMemoryConfigUsesProviderMaintenanceDefault(t *testing.T) {
	got, ok := ResolveCodingAgentMemoryConfig(providerProfile("claude-code"))
	if !ok || got.Provider != "claude-code" || got.ModelID != "claude-sonnet-5-5" || got.Options["reasoning_effort"] != "high" {
		t.Fatalf("ResolveCodingAgentMemoryConfig() = %+v, %v", got, ok)
	}
}
