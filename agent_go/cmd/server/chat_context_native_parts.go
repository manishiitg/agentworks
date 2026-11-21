package server

import (
	"context"
	"strconv"
	"strings"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

// nativeChatContextParts sizes what fills a Claude Code chat from its own transcript: Claude Code reports only the
// total, so this is the one way to show what the total is made of. nil for other coding agents or when the
// transcript cannot be read.
func (api *StreamingAPI) nativeChatContextParts(owner string, tab chatContextTab, usedTokens int) []chatContextPart {
	runtime := chatContextRuntimeMetadata(owner, tab.Record)
	if runtime == nil || runtime.AgentSessionHandle == nil {
		return nil
	}
	handle := runtime.AgentSessionHandle
	provider := chatProviderKey(firstNonEmptyTrimmed(handle.Provider.Provider, runtime.Provider))
	if provider != chatProviderKey("claude-code") {
		return nil
	}
	nativeID := firstNonEmptyTrimmed(handle.Provider.NativeSessionID, runtime.ExternalSessionID)
	if nativeID == "" {
		return nil
	}
	accountHome := ""
	if handle.ConnectionID != "" {
		scope := providerAccountScope{Principal: owner, WorkspacePath: tab.Record.WorkspacePath}
		if keys, err := api.connectionAPIKeys(context.Background(), scope, "claude-code", handle.ConnectionID); err == nil && keys != nil {
			accountHome = keys.RuntimeEnvironment["HOME"]
		}
	}
	breakdown, ok := claudecode.ContextBreakdownFromTranscript(nativeID, strings.TrimSpace(handle.Provider.WorkingDir), accountHome)
	if !ok {
		return nil
	}
	var ours int
	if sizes, known := api.promptParts.Load(tab.Record.SessionID); known {
		s := sizes.(mcpagent.PromptPartSizes)
		ours = (s.Instructions + s.Added + s.Runtime) / charsPerToken
	}
	return nativeContextParts(breakdown, ours, usedTokens)
}

// nativeContextParts turns a transcript breakdown into the pane's parts, scaled to the total the chat reports now.
// ours is the size of the instructions AgentWorks put in the setup, when known.
func nativeContextParts(b claudecode.ContextBreakdown, ours, usedTokens int) []chatContextPart {
	if b.Total <= 0 {
		return nil
	}
	scale := func(n int) int {
		if usedTokens <= 0 {
			return n
		}
		return int(int64(n) * int64(usedTokens) / int64(b.Total))
	}
	var parts []chatContextPart
	add := func(key, label string, tokens int, note string) {
		if tokens = scale(tokens); tokens > 0 {
			parts = append(parts, chatContextPart{Key: key, Label: label, Tokens: tokens, Note: note})
		}
	}
	setup := b.Setup
	if ours > 0 && ours < setup {
		add("instructions", "Your instructions", ours, "")
		setup -= ours
	}
	add("app_setup", "Claude Code's own rules and tools", setup, "")
	add("skills", "Skills list", b.Skills, "")
	add("files", "Instruction files (CLAUDE.md, AGENTS.md)", b.InstructionFiles, "")
	if b.ResentCount > 0 {
		note := "Your instructions changed while this chat was open, so they were sent again in a message. Each copy stays until the chat compacts."
		if b.ResentCount > 1 {
			note = "Your instructions changed while this chat was open, so they were sent again in messages. Each copy stays until the chat compacts."
		}
		label := "Instructions sent again"
		if b.ResentCount > 1 {
			label = "Instructions sent again (" + strconv.Itoa(b.ResentCount) + " times)"
		}
		add("resent", label, b.Resent, note)
	}
	add("summary", "Summary of the earlier chat", b.Summary, "")
	add("notes", "Reminders Claude Code adds", b.AppNotes, "")
	// The conversation takes what is left, so the parts add up to the reported total.
	target := usedTokens
	if target <= 0 {
		target = b.Total
	}
	rest := target
	for _, part := range parts {
		rest -= part.Tokens
	}
	if rest > 0 {
		parts = append(parts, chatContextPart{Key: "messages", Label: "Conversation", Tokens: rest})
	}
	return parts
}
