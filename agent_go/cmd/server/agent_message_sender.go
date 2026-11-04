package server

import (
	"context"
	"strings"
)

// agentMessageSender names who sent an agent message and through what, in plain words, for the receiving chat's
// "From …" line (owner, 5 Nov: "it shows From Crew · test, it should show which agent also, or web or mcp etc"):
// agent is which agent (and whose, when it is another person's), channel how it came. No ids.
func agentMessageSender(ctx context.Context, source agentMessageEndpoint, receiver string) (label, agent, channel string) {
	switch {
	case source.NamedAgentID != "":
		agent, channel = "an agent", "AgentBridge"
		if named, err := namedAgentByID(ctx, source.UserID, source.NamedAgentID); err == nil {
			name := firstNonEmptyTrimmed(named.Label, named.Name)
			agent = name
			if named.Attachment != nil {
				if program := attachedProgramName(named.Attachment.Provider); program != "" {
					agent = program + " (" + name + ")"
				}
			}
		}
	case source.ConditionJob != "":
		agent, channel = firstNonEmptyTrimmed(source.Label, "A watcher"), "watcher"
	case source.Kind == triggerCallerCrew:
		name := "Main"
		if strings.Contains(source.ChatKey, codeChatSideMarker) {
			name = "an agent"
			if identity, err := defaultProductConversationRegistryStore().agentIdentity(ctx, source.UserID, source.Profile, source.ChatKey); err == nil && strings.TrimSpace(identity.Name) != "" {
				name = strings.TrimSpace(identity.Name)
			}
		}
		agent, channel = name, "chat"
		if crew := strings.TrimSpace(source.Label); crew != "" {
			agent += " in " + crew
		}
		if source.External {
			channel = "MCP"
		}
	case source.Kind == triggerCallerWorkflow:
		agent, channel = firstNonEmptyTrimmed(source.Label, "A Goal"), "Goal"
		if agentMessageSenderKindIn(ctx, source) == "flow" {
			channel = "Flow"
		}
	default: // a person's own AI app over MCP: its connection is named after the app ("ChatGPT")
		agent, channel = externalConnectionApp(source.Label), "MCP"
	}
	if source.UserID != "" && receiver != "" && source.UserID != receiver && source.Kind != triggerCallerCrew && source.Kind != triggerCallerWorkflow {
		agent = shareOwnerLabel(source.UserID) + "'s " + agent
	}
	return agent + " · " + channel, agent, channel
}

// attachedProgramName is the coding app an attached chat runs in, as people call it.
func attachedProgramName(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "cursor":
		return "Cursor"
	case "muse":
		return "Muse"
	case "pi":
		return "Pi"
	case "agy":
		return "Antigravity"
	}
	return ""
}

// externalConnectionApp reads the app's name from an external connection's label ("external connection of ann
// (ChatGPT)"), else says "an AI app".
func externalConnectionApp(label string) string {
	label = strings.TrimSpace(label)
	if at := strings.LastIndex(label, " ("); at >= 0 && strings.HasSuffix(label, ")") {
		if app := strings.TrimSpace(label[at+2 : len(label)-1]); app != "" {
			return app
		}
	}
	if label != "" && !strings.HasPrefix(label, "external connection of ") {
		return label
	}
	return "an AI app"
}
