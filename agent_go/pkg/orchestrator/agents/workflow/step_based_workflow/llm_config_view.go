package step_based_workflow

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

type resolvedLLMRole struct {
	Role     string
	Config   *workflowtypes.AgentLLMConfig
	Source   string
	Override bool
}

func reasoningEffort(options map[string]interface{}) string {
	if value, ok := options["reasoning_effort"].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return "default"
}

func resolvedWorkflowLLMRoles(manifestJSON string) ([]resolvedLLMRole, error) {
	var manifest struct {
		Capabilities struct {
			LLMConfig workflowtypes.PresetLLMConfig `json:"llm_config"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(manifestJSON), &manifest); err != nil {
		return nil, err
	}
	cfg := &manifest.Capabilities.LLMConfig
	workflowtypes.NormalizePresetLLMConfig(cfg)
	if cfg.Mode == workflowtypes.LLMConfigModeProviderProfile {
		builder, tiers, ok := workflowtypes.ResolveProviderProfileConfig(cfg)
		if !ok || builder == nil || tiers == nil {
			return nil, fmt.Errorf("provider profile %q has no complete model defaults", cfg.Provider)
		}
		pulse, _ := workflowtypes.ResolveProviderProfileBackgroundConfig(cfg)
		source := "provider_profile:" + cfg.Provider
		return []resolvedLLMRole{
			{"builder", builder, source, false},
			{"execution_high", tiers.Tier1, source, false},
			{"execution_medium", tiers.Tier2, source, false},
			{"execution_low", tiers.Tier3, source, false},
			{"background", pulse, source, false},
		}, nil
	}
	if cfg.TieredConfig == nil {
		return nil, fmt.Errorf("explicit LLM config has no execution tiers")
	}
	return []resolvedLLMRole{
		{"builder", cfg.BuilderLLM, "explicit_override", true},
		{"execution_high", cfg.TieredConfig.Tier1, "explicit_override", true},
		{"execution_medium", cfg.TieredConfig.Tier2, "explicit_override", true},
		{"execution_low", cfg.TieredConfig.Tier3, "explicit_override", true},
		{"background", cfg.BackgroundLLM, "explicit_override", true},
	}, nil
}

func renderResolvedWorkflowLLMRoles(manifestJSON string) (string, error) {
	roles, err := resolvedWorkflowLLMRoles(manifestJSON)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	out.WriteString("## Effective LLM Configuration\n\n")
	out.WriteString("| Role | Provider / model | Reasoning | Source | Override |\n")
	out.WriteString("|---|---|---|---|---|\n")
	for _, role := range roles {
		providerModel := "unresolved"
		reasoning := "default"
		if role.Config != nil {
			providerModel = strings.TrimSpace(role.Config.Provider) + "/" + strings.TrimSpace(role.Config.ModelID)
			reasoning = reasoningEffort(role.Config.Options)
		}
		out.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %t |\n", role.Role, providerModel, reasoning, role.Source, role.Override))
	}
	return out.String(), nil
}
