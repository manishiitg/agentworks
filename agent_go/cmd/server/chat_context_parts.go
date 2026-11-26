package server

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// chatContextPart is one slice of what fills a chat's context. Tokens are an estimate (about four characters
// each): the model reports its total, not the size of each part.
type chatContextPart struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Tokens int    `json:"tokens"`
	// Note explains a part people can act on (for example instructions sent more than once).
	Note string `json:"note,omitempty"`
	// Openable means the pane can list this part's items with their text (GET /api/chat-context/part).
	Openable bool `json:"openable,omitempty"`
}

// charsPerToken is the rough size of a token used to turn part sizes into the estimate shown.
const charsPerToken = 4

// chatContextParts splits a chat's reported context into what the product sends every time (instructions,
// skills, tools) and the conversation, which is whatever is left of the total. nil when the parts are not
// known or do not fit inside the total (the estimate would contradict the real figure).
func chatContextParts(sizes mcpagent.PromptPartSizes, usedTokens int) []chatContextPart {
	instructions := (sizes.Instructions + sizes.Added + sizes.Runtime) / charsPerToken
	skills := sizes.Skills / charsPerToken
	tools := sizes.Tools / charsPerToken
	fixed := instructions + skills + tools
	if usedTokens <= 0 || fixed <= 0 || fixed > usedTokens {
		return nil
	}
	parts := []chatContextPart{{Key: "instructions", Label: "Instructions", Tokens: instructions}}
	if skills > 0 {
		parts = append(parts, chatContextPart{Key: "skills", Label: "Skills", Tokens: skills})
	}
	if tools > 0 {
		parts = append(parts, chatContextPart{Key: "tools", Label: "Tools", Tokens: tools})
	}
	return append(parts, chatContextPart{Key: "messages", Label: "Conversation", Tokens: usedTokens - fixed})
}

// labelChatCost names each period's apps and adds what cache reads saved, for
// models whose list prices are known.
func labelChatCost(cost *costledger.SessionCost) {
	for _, period := range []*costledger.SessionCostPeriod{&cost.Current, cost.Earlier} {
		if period == nil {
			continue
		}
		period.Apps = period.Apps[:0]
		for _, provider := range period.Providers {
			period.Apps = append(period.Apps, providerDisplayLabel(provider))
		}
		period.CacheSavedUSD = 0
		for i := range period.Models {
			model := &period.Models[i]
			model.App = providerDisplayLabel(model.Provider)
			model.Name = orchestrator.ModelDisplayName(model.Provider, model.Model)
			if saved, ok := orchestrator.CacheSavedUSD(model.Provider, model.Model, model.Tokens.CacheRead); ok {
				model.CacheSavedUSD = saved
				period.CacheSavedUSD += saved
			}
		}
	}
}
