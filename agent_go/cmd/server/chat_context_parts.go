package main

import (
	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// chatContextPart is one slice of what fills a chat's context. Tokens are an estimate (about four characters
// each): the model reports its total, not the size of each part.
type chatContextPart struct {
	Name   string `json:"name"`
	Tokens int    `json:"tokens"`
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
	parts := []chatContextPart{{Name: "Instructions", Tokens: instructions}}
	if skills > 0 {
		parts = append(parts, chatContextPart{Name: "Skills", Tokens: skills})
	}
	if tools > 0 {
		parts = append(parts, chatContextPart{Name: "Tools", Tokens: tools})
	}
	return append(parts, chatContextPart{Name: "Conversation", Tokens: usedTokens - fixed})
}
