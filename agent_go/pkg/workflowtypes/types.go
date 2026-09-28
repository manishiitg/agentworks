// Package workflowtypes holds the shared type definitions and constants for
// workflow configuration: LLM config (primary + tiered), workflow phase
// status, and phase option selections. Everything here is pure data —
// no persistence, no SQL.
package workflowtypes

import (
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

// Workflow status/phase constants.
const (
	WorkflowStatusPreVerification  = "execution"
	WorkflowStatusPostVerification = "post-verification"
	WorkflowStatusWorkflowBuilder  = "workflow-builder"
)

const (
	LLMConfigSchemaVersion       = 2
	LLMConfigModeProviderProfile = "provider_profile"
	LLMConfigModeExplicit        = "explicit"
)

// Knowledgebase shape — only one shape supported: notes-only (per-topic
// markdown files under knowledgebase/notes/ plus notes/_index.json). The
// legacy graph+notes shape has been removed. KBShapeGraphNotes is kept as a
// recognized-but-deprecated value so existing workflow configs don't fail to
// parse; everything resolves to KBShapeNotesOnly at runtime.
const (
	// KBShapeNotesOnly — per-topic markdown files + notes/_index.json registry.
	// The only supported shape.
	KBShapeNotesOnly = "notes-only"
	// KBShapeGraphNotes is retained as a recognized legacy value for config
	// compatibility. ResolveKBShape collapses it to KBShapeNotesOnly.
	KBShapeGraphNotes = "graph+notes"
)

// ValidKBShape reports whether s is a recognized kb_shape value. Empty is valid
// (resolves to notes-only). Legacy "graph+notes" is accepted but collapsed to
// notes-only at runtime.
func ValidKBShape(s string) bool {
	switch s {
	case "", KBShapeGraphNotes, KBShapeNotesOnly:
		return true
	}
	return false
}

// ResolveKBShape always returns KBShapeNotesOnly. The graph surface has been
// removed; empty and legacy "graph+notes" values both resolve here for
// backward compatibility with configs on disk, but the runtime behavior is
// always notes-only.
func ResolveKBShape(s string) string {
	_ = s
	return KBShapeNotesOnly
}

// PresetLLMConfig represents LLM configuration stored with workflow presets.
type PresetLLMConfig struct {
	SchemaVersion int    `json:"schema_version"`
	Mode          string `json:"mode"`

	// Provider is stored only in provider_profile mode. The provider package
	// owns the Builder, execution-tier, and Pulse defaults and can
	// evolve them when the application is updated.
	Provider     string `json:"provider,omitempty"`
	ConnectionID string `json:"connection_id,omitempty"`

	// Explicit mode pins each workflow role directly.
	BuilderLLM *AgentLLMConfig `json:"builder_llm,omitempty"`

	// Optional Pulse Gate/routine post-run QA override. When omitted,
	// coding-agent providers may supply a provider-owned Pulse default.
	PulseLLM *AgentLLMConfig `json:"pulse_llm,omitempty"`

	// Feature toggles.
	UseKnowledgebase *bool  `json:"use_knowledgebase,omitempty"`
	KBShape          string `json:"kb_shape,omitempty"` // "graph+notes" (default) | "notes-only"

	TieredConfig *TieredLLMConfig `json:"tiered_config,omitempty"`

	// Migration-only fields. NormalizePresetLLMConfig consumes and clears these
	// when an older workflow is loaded, so they are never written again.
	LegacyModelID           string          `json:"model_id,omitempty"`
	LegacyLockKnowledgebase *bool           `json:"lock_knowledgebase,omitempty"`
	LegacyPhaseLLM          *AgentLLMConfig `json:"phase_llm,omitempty"`
	LegacyAutoImproveLLM    *AgentLLMConfig `json:"auto_improve_llm,omitempty"`
	LegacyLLMAllocationMode string          `json:"llm_allocation_mode,omitempty"`
}

// TieredLLMConfig represents the 3-tier LLM configuration for tiered allocation mode.
type TieredLLMConfig struct {
	Tier1 *AgentLLMConfig `json:"tier_1"` // High reasoning
	Tier2 *AgentLLMConfig `json:"tier_2"` // Medium reasoning
	Tier3 *AgentLLMConfig `json:"tier_3"` // Low reasoning
}

// AgentLLMConfig represents LLM configuration for a specific agent type.
type AgentLLMConfig struct {
	ConnectionID   string                 `json:"connection_id,omitempty"`
	PublishedLLMID string                 `json:"published_llm_id,omitempty"`
	Provider       string                 `json:"provider"`
	ModelID        string                 `json:"model_id"`
	Options        map[string]interface{} `json:"options,omitempty"`
}

func agentLLMConfigFromCodingAgentRef(ref llmproviders.CodingAgentTierModelRef) *AgentLLMConfig {
	if ref.Provider == "" || ref.ModelID == "" {
		return nil
	}
	return &AgentLLMConfig{
		Provider: ref.Provider,
		ModelID:  ref.ModelID,
		Options:  ref.Options,
	}
}

// NormalizePresetLLMConfig converts the retired workflow LLM shapes once and
// returns true when the caller should persist the normalized config.
func NormalizePresetLLMConfig(config *PresetLLMConfig) bool {
	if config == nil {
		return false
	}

	changed := false
	legacyMode := config.LegacyLLMAllocationMode
	if config.Mode == "" {
		legacyHasOverrides := config.LegacyPhaseLLM != nil || config.LegacyAutoImproveLLM != nil || config.PulseLLM != nil || config.TieredConfig != nil
		if (legacyMode == "coding_agent" || legacyMode == "coding_plan") && config.Provider != "" && !legacyHasOverrides {
			config.Mode = LLMConfigModeProviderProfile
		} else {
			config.Mode = LLMConfigModeExplicit
		}
		changed = true
	}

	if config.Mode == LLMConfigModeExplicit {
		var providerDefaults *llmproviders.CodingAgentDefaultTierModels
		if config.Provider != "" {
			providerDefaults, _ = llmproviders.GetCodingAgentDefaultTierModels(llmproviders.Provider(config.Provider))
		}
		if config.BuilderLLM == nil {
			switch {
			case config.LegacyPhaseLLM != nil:
				config.BuilderLLM = config.LegacyPhaseLLM
			case config.Provider != "" && config.LegacyModelID != "":
				config.BuilderLLM = &AgentLLMConfig{Provider: config.Provider, ModelID: config.LegacyModelID}
			case providerDefaults != nil:
				config.BuilderLLM = agentLLMConfigFromCodingAgentRef(providerDefaults.Builder)
			}
			if config.BuilderLLM != nil {
				changed = true
			}
		}
		if config.TieredConfig == nil && config.BuilderLLM != nil {
			if providerDefaults != nil {
				config.TieredConfig = &TieredLLMConfig{
					Tier1: agentLLMConfigFromCodingAgentRef(providerDefaults.High),
					Tier2: agentLLMConfigFromCodingAgentRef(providerDefaults.Medium),
					Tier3: agentLLMConfigFromCodingAgentRef(providerDefaults.Low),
				}
			} else {
				config.TieredConfig = &TieredLLMConfig{
					Tier1: config.BuilderLLM,
					Tier2: config.BuilderLLM,
					Tier3: config.BuilderLLM,
				}
			}
			changed = true
		}
		defaultPulse := config.BuilderLLM
		if config.TieredConfig != nil && config.TieredConfig.Tier1 != nil {
			defaultPulse = config.TieredConfig.Tier1
		}
		if config.PulseLLM == nil {
			if config.LegacyAutoImproveLLM != nil {
				config.PulseLLM = config.LegacyAutoImproveLLM
			} else if providerDefaults != nil {
				config.PulseLLM = agentLLMConfigFromCodingAgentRef(providerDefaults.Pulse)
			} else {
				config.PulseLLM = defaultPulse
			}
			if config.PulseLLM != nil {
				changed = true
			}
		}
		if config.Provider != "" {
			config.Provider = ""
			changed = true
		}
	} else if config.Mode == LLMConfigModeProviderProfile {
		if config.BuilderLLM != nil || config.PulseLLM != nil || config.TieredConfig != nil {
			config.BuilderLLM = nil
			config.PulseLLM = nil
			config.TieredConfig = nil
			changed = true
		}
	}

	if config.SchemaVersion != LLMConfigSchemaVersion {
		config.SchemaVersion = LLMConfigSchemaVersion
		changed = true
	}
	if config.LegacyModelID != "" || config.LegacyPhaseLLM != nil || config.LegacyAutoImproveLLM != nil || config.LegacyLLMAllocationMode != "" {
		config.LegacyModelID = ""
		config.LegacyPhaseLLM = nil
		config.LegacyAutoImproveLLM = nil
		config.LegacyLLMAllocationMode = ""
		changed = true
	}
	// PLAT-265 retired the workflow-wide KB lock. KB writes are governed only
	// by each step's knowledgebase_access + knowledgebase_contribution contract.
	// Consume the old JSON field during manifest normalization so workflows do
	// not keep advertising a setting that no longer has runtime meaning.
	if config.LegacyLockKnowledgebase != nil {
		config.LegacyLockKnowledgebase = nil
		changed = true
	}
	return changed
}

// ResolveProviderProfileConfig expands a provider profile into current package
// defaults. It intentionally does not persist the resolved models.
func ResolveProviderProfileConfig(config *PresetLLMConfig) (*AgentLLMConfig, *TieredLLMConfig, bool) {
	if config == nil || config.Mode != LLMConfigModeProviderProfile || config.Provider == "" {
		return nil, nil, false
	}

	defaults, ok := llmproviders.GetCodingAgentDefaultTierModels(llmproviders.Provider(config.Provider))
	if !ok {
		return nil, nil, false
	}

	builder := agentLLMConfigFromCodingAgentRef(defaults.Builder)
	tiered := &TieredLLMConfig{
		Tier1: agentLLMConfigFromCodingAgentRef(defaults.High),
		Tier2: agentLLMConfigFromCodingAgentRef(defaults.Medium),
		Tier3: agentLLMConfigFromCodingAgentRef(defaults.Low),
	}

	for _, role := range []*AgentLLMConfig{builder, tiered.Tier1, tiered.Tier2, tiered.Tier3} {
		if role != nil {
			role.ConnectionID = config.ConnectionID
		}
	}
	if tiered.Tier1 == nil || tiered.Tier2 == nil || tiered.Tier3 == nil {
		return builder, nil, true
	}
	return builder, tiered, true
}

func ResolveProviderProfilePulseConfig(config *PresetLLMConfig) (*AgentLLMConfig, bool) {
	if config == nil || config.Provider == "" {
		return nil, false
	}

	defaults, ok := llmproviders.GetCodingAgentDefaultTierModels(llmproviders.Provider(config.Provider))
	if !ok {
		return nil, false
	}
	pulse := agentLLMConfigFromCodingAgentRef(defaults.Pulse)
	if pulse == nil {
		pulse = agentLLMConfigFromCodingAgentRef(defaults.High)
	}
	if pulse == nil {
		return nil, false
	}
	pulse.ConnectionID = config.ConnectionID
	return pulse, true
}

// ResolveCodingAgentMemoryConfig returns the model used by scheduled memory
// enrichment. Memory follows the Pulse default because it is frequent,
// report-free background maintenance.
func ResolveCodingAgentMemoryConfig(config *PresetLLMConfig) (*AgentLLMConfig, bool) {
	return ResolveProviderProfilePulseConfig(config)
}

// WorkflowSelectedOption represents a selected option for a workflow phase.
type WorkflowSelectedOption struct {
	OptionID    string `json:"option_id"`
	OptionLabel string `json:"option_label"`
	OptionValue string `json:"option_value"`
	Group       string `json:"group"`
	PhaseID     string `json:"phase_id"`
}

// WorkflowSelectedOptions represents all selected options for a workflow phase.
type WorkflowSelectedOptions struct {
	PhaseID    string                   `json:"phase_id"`
	Selections []WorkflowSelectedOption `json:"selections"`
}

func ValidatePresetLLMConfigPublic(config *PresetLLMConfig) error {
	NormalizePresetLLMConfig(config)
	if config.Mode == LLMConfigModeProviderProfile {
		if config.Provider == "" {
			return fmt.Errorf("provider is required for provider_profile llm_config")
		}
		if _, ok := llmproviders.GetCodingAgentDefaultTierModels(llmproviders.Provider(config.Provider)); !ok {
			return fmt.Errorf("provider %q does not expose coding agent tier defaults", config.Provider)
		}
		return nil
	}
	if config.Mode != LLMConfigModeExplicit {
		return fmt.Errorf("llm_config mode must be %q or %q", LLMConfigModeProviderProfile, LLMConfigModeExplicit)
	}
	if err := validateRequiredPresetAgentLLMConfig(config.BuilderLLM, "builder_llm"); err != nil {
		return err
	}
	if config.TieredConfig == nil {
		return fmt.Errorf("tiered_config is required in explicit mode")
	}
	{
		tierConfigs := []struct {
			config *AgentLLMConfig
			name   string
		}{
			{config.TieredConfig.Tier1, "tier_1"},
			{config.TieredConfig.Tier2, "tier_2"},
			{config.TieredConfig.Tier3, "tier_3"},
		}
		for _, tierConfig := range tierConfigs {
			if tierConfig.config == nil {
				return fmt.Errorf("%s is required in tiered_config", tierConfig.name)
			}
			if tierConfig.config.ModelID == "" {
				return fmt.Errorf("model_id is required for %s", tierConfig.name)
			}
			if tierConfig.config.Provider == "" {
				return fmt.Errorf("provider is required for %s", tierConfig.name)
			}
			if err := validateCodingAgentProvider(tierConfig.config.Provider, tierConfig.name); err != nil {
				return err
			}
		}
	}
	if err := validateRequiredPresetAgentLLMConfig(config.PulseLLM, "pulse_llm"); err != nil {
		return err
	}
	return nil
}

func validateRequiredPresetAgentLLMConfig(config *AgentLLMConfig, name string) error {
	if config == nil {
		return fmt.Errorf("%s is required", name)
	}
	return validatePresetAgentLLMConfig(config, name)
}

func validatePresetAgentLLMConfig(config *AgentLLMConfig, name string) error {
	if config == nil {
		return nil
	}
	if config.ModelID == "" {
		return fmt.Errorf("model_id is required for %s", name)
	}
	return validateCodingAgentProvider(config.Provider, name)
}

// validateCodingAgentProvider accepts only coding-agent CLI providers; direct
// API transports are no longer supported.
func validateCodingAgentProvider(provider, name string) error {
	if _, err := llmproviders.ValidateProvider(provider); err != nil {
		return fmt.Errorf("invalid provider for %s: %w", name, err)
	}
	if !llmproviders.IsCodingAgentProvider(llmproviders.Provider(strings.ToLower(strings.TrimSpace(provider))), "") {
		return fmt.Errorf("provider %q for %s is not supported: only coding-agent CLIs can run agents", provider, name)
	}
	if err := llmguard.RequireCodingAgentProvider(provider); err != nil {
		return fmt.Errorf("provider for %s: %w", name, err)
	}
	return nil
}
