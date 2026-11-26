package mcpagent

import (
	"context"
	"encoding/json"

	llm "github.com/manishiitg/multi-llm-provider-go"
)

// PromptPartSizes is how big each part of what this agent sends before the
// conversation is, in characters. Callers turn characters into an estimate;
// the model's own token counts are not available per part.
type PromptPartSizes struct {
	// Instructions is the caller's base prompt (identity, project rules).
	Instructions int `json:"instructions"`
	// Added is the supplementary instructions appended to the base prompt.
	Added int `json:"added"`
	// Skills is the skill listing placed in the prompt (0 for coding agents
	// that read skills from disk).
	Skills int `json:"skills"`
	// Runtime is the rest of the system prompt: tool-routing guidance.
	Runtime int `json:"runtime"`
	// Tools is the size of the tool definitions sent with each request.
	Tools int `json:"tools"`
}

// PromptPartSizes measures the parts of the next request. It reads current
// state only and never changes the agent.
func (a *Agent) PromptPartSizes(ctx context.Context) PromptPartSizes {
	if a == nil {
		return PromptPartSizes{}
	}
	sizes := PromptPartSizes{Instructions: len(a.systemPrompt)}
	for _, extra := range a.appendedSystemPrompts {
		sizes.Added += len(extra)
	}
	if !llm.IsCodingAgentProvider(a.provider, a.modelID) || a.provider == llm.ProviderAgyCLI {
		sizes.Skills = len(renderSkillListing(a.attachedSkills))
	}
	if rest := len(a.outgoingSystemPromptForContext(ctx)) - sizes.Instructions - sizes.Added - sizes.Skills; rest > 0 {
		sizes.Runtime = rest
	}
	if len(a.filteredTools) > 0 {
		if encoded, err := json.Marshal(a.filteredTools); err == nil {
			sizes.Tools = len(encoded)
		}
	}
	return sizes
}
