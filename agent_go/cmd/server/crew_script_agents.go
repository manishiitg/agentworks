package server

import (
	"encoding/json"
	"strings"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// outlineAgent is what the Steps page prints for an agent: the server's
// resolution of the model a run would use. Source is "default" when the workflow's
// own settings decide it and "call" when the agent() call names a model, tier or
// reasoning itself.
type outlineAgent struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Reasoning string `json:"reasoning,omitempty"`
	Source    string `json:"source"`
}

// outlineTiers expands the workflow's saved model settings into its execution
// tiers, the same way a run does (provider profile or explicit tiered_config).
func outlineTiers(config *workflowtypes.PresetLLMConfig) *workflowtypes.TieredLLMConfig {
	if config == nil {
		return nil
	}
	copied := *config
	workflowtypes.NormalizePresetLLMConfig(&copied)
	if copied.Mode == workflowtypes.LLMConfigModeProviderProfile {
		_, tiers, _ := workflowtypes.ResolveProviderProfileConfig(&copied)
		return tiers
	}
	return copied.TieredConfig
}

func providerLabel(provider string) string {
	switch provider {
	case "claude-code":
		return "Claude"
	case "codex-cli":
		return "Codex"
	case "cursor-cli":
		return "Cursor"
	case "pi-cli":
		return "Pi"
	}
	name := strings.TrimSuffix(strings.TrimSuffix(provider, "-cli"), "-code")
	if name == "" {
		return provider
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// resolveOutlineAgent follows the run's rule (python_agent.go): a call's model
// wins, else its tier, else the default High tier; a call's reasoning replaces
// the model's saved reasoning. Anything it cannot resolve returns nil: never a guess.
func resolveOutlineAgent(tiers *workflowtypes.TieredLLMConfig, model, tier, reasoning string) *outlineAgent {
	result := &outlineAgent{Source: "default"}
	switch {
	case model != "":
		provider, id, ok := strings.Cut(model, "/")
		if !ok || provider == "" || id == "" || tier != "" {
			return nil
		}
		result.Provider, result.Model, result.Source = strings.ToLower(strings.TrimSpace(provider)), id, "call"
	case tiers != nil:
		level := workflow.TierHigh
		if tier != "" {
			var ok bool
			if level, ok = workflow.ParseTierOverride(tier); !ok {
				return nil
			}
			result.Source = "call"
		}
		chosen := map[workflow.TierLevel]*workflowtypes.AgentLLMConfig{workflow.TierHigh: tiers.Tier1, workflow.TierMedium: tiers.Tier2, workflow.TierLow: tiers.Tier3}[level]
		if chosen == nil || chosen.Provider == "" || chosen.ModelID == "" {
			return nil
		}
		result.Provider, result.Model = chosen.Provider, chosen.ModelID
		result.Reasoning, _ = chosen.Options["reasoning_effort"].(string)
		result.Reasoning = strings.TrimSpace(result.Reasoning)
	default:
		return nil
	}
	if reasoning != "" {
		result.Reasoning, result.Source = reasoning, "call"
	}
	// The name the model picker shows, when the platform's catalog knows the id; otherwise the id as saved.
	for _, known := range providerModelMetadata(result.Provider) {
		if known.ModelID == result.Model && strings.TrimSpace(known.ModelName) != "" {
			result.Model = strings.TrimSpace(known.ModelName)
			break
		}
	}
	result.Provider = providerLabel(result.Provider)
	return result
}

// addOutlineModels adds default_agent to the outline and agent to every agent row.
func addOutlineModels(graph json.RawMessage, config *workflowtypes.PresetLLMConfig) (json.RawMessage, error) {
	var outline map[string]json.RawMessage
	if err := json.Unmarshal(graph, &outline); err != nil {
		return nil, err
	}
	tiers := outlineTiers(config)
	if fallback := resolveOutlineAgent(tiers, "", "", ""); fallback != nil {
		outline["default_agent"], _ = json.Marshal(fallback)
	}
	var nodes []map[string]interface{}
	if json.Unmarshal(outline["nodes"], &nodes) != nil {
		return graph, nil
	}
	for _, node := range nodes {
		if node["type"] != "agent" {
			continue
		}
		set, _ := node["settings"].(map[string]interface{})
		delete(node, "settings")
		unresolved := false
		for _, key := range []string{"model", "tier", "reasoning"} {
			if value, present := set[key]; present {
				if _, known := value.(string); !known {
					unresolved = true
				}
			}
		}
		if unresolved {
			delete(node, "agent")
			continue
		}
		pick := func(key string) string { value, _ := set[key].(string); return strings.TrimSpace(value) }
		if agent := resolveOutlineAgent(tiers, pick("model"), pick("tier"), pick("reasoning")); agent != nil {
			node["agent"] = agent
		}
	}
	outline["nodes"], _ = json.Marshal(nodes)
	return json.Marshal(outline)
}
