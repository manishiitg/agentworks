package workflowtypes

import "testing"

func testExplicitConfig() *PresetLLMConfig {
	builder := &AgentLLMConfig{Provider: "claude-code", ModelID: "claude-sonnet-5-5"}
	high := &AgentLLMConfig{Provider: "claude-code", ModelID: "claude-opus-4-8"}
	return &PresetLLMConfig{
		SchemaVersion: LLMConfigSchemaVersion,
		Mode:          LLMConfigModeExplicit,
		BuilderLLM:    builder,
		PulseLLM:      builder,
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
	if cfg.BuilderLLM != legacyBuilder || cfg.PulseLLM != legacyMaintenance {
		t.Fatalf("normalized roles = builder:%+v pulse:%+v", cfg.BuilderLLM, cfg.PulseLLM)
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

// Claude Code's expected model/effort here must track
// multi-llm-provider-go's GetCodingAgentDefaultTierModels for
// ProviderClaudeCode. Fixed 2026-09-11: this test still expected
// claude-fable-5-1/claude-opus-5 after multi-llm-provider-go's 3fc07b8
// ("Revert Claude Code Builder tier back to Sonnet 5.5 / high") reverted
// Builder/High/Pulse to claude-sonnet-5-5 -- a cross-repo staleness (the
// replace directive always builds against whatever's checked out at
// ../../multi-llm-provider-go), not something wrong with the model choice
// itself; verify against that package before changing these expectations
// again.
func TestResolveProviderProfileConfigUsesBuilderDefaults(t *testing.T) {
	tests := []struct {
		provider string
		model    string
		effort   string
	}{
		{provider: "claude-code", model: "claude-sonnet-5-5", effort: "high"},
		{provider: "codex-cli", model: "gpt-6-astra", effort: "medium"},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			builder, _, ok := ResolveProviderProfileConfig(providerProfile(tt.provider))
			if !ok || builder == nil {
				t.Fatalf("ResolveProviderProfileConfig(%q) = %+v, ok=%v", tt.provider, builder, ok)
			}
			if builder.Provider != tt.provider || builder.ModelID != tt.model {
				t.Fatalf("builder = %+v, want %s/%s", builder, tt.provider, tt.model)
			}
			if got := builder.Options["reasoning_effort"]; got != tt.effort {
				t.Fatalf("reasoning_effort = %#v, want %q", got, tt.effort)
			}
		})
	}
}

func TestResolveProviderProfilePulseConfigUsesProviderDefault(t *testing.T) {
	got, ok := ResolveProviderProfilePulseConfig(providerProfile("claude-code"))
	if !ok || got.Provider != "claude-code" || got.ModelID != "claude-sonnet-5-5" || got.Options["reasoning_effort"] != "high" {
		t.Fatalf("ResolveProviderProfilePulseConfig() = %+v, %v", got, ok)
	}
}

func TestResolveCodingAgentMemoryConfigUsesPulseDefault(t *testing.T) {
	got, ok := ResolveCodingAgentMemoryConfig(providerProfile("claude-code"))
	if !ok || got.Provider != "claude-code" || got.ModelID != "claude-sonnet-5-5" || got.Options["reasoning_effort"] != "high" {
		t.Fatalf("ResolveCodingAgentMemoryConfig() = %+v, %v", got, ok)
	}
}
