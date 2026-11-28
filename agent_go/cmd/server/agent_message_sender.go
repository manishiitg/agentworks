package server

import (
	"context"
	"strings"
)

// agentMessageSender is the server-owned person and current agent name for a
// chat row. Transport/routing metadata stays separate, never in the name.
func agentMessageSender(ctx context.Context, source agentMessageEndpoint, receiver string) (label, agent, channel string) {
	// A CLI's authenticated bound-agent identity is stronger than its MCP
	// connection label. Use it for display only, never alter routing authority.
	if source.NamedAgentID == "" {
		if claims := GetUserFromContext(ctx); claims != nil && claims.UserID == source.UserID && claims.AccessToken != nil && claims.AccessToken.BoundAgentID != "" {
			source.NamedAgentID = claims.AccessToken.BoundAgentID
		}
	}
	agent, channel = "Agent", "AgentBridge"
	// A Watcher's event is an automatic message, not the Crew's own chat: the row says so
	// (it used to read "<person> · <agent>" because the caller is stamped with the Crew).
	if source.ConditionJob != "" {
		name := agentMessageReadableName(strings.TrimPrefix(strings.TrimPrefix(source.Label, "Watcher: "), "Condition: "))
		if name == "Agent" {
			name = "unnamed"
		}
		return "Watcher: " + name, "Watcher", channel
	}
	switch {
	case source.TeamAgentID != "":
		if team, err := readCrewTeam(source.ID); err == nil {
			if row, ok := team.Agents[source.TeamAgentID]; ok && row.OwnerID == source.UserID {
				agent = row.Name
			}
		}
	case source.NamedAgentID != "":
		if named, err := namedAgentByID(ctx, source.UserID, source.NamedAgentID); err == nil {
			agent = firstNonEmptyTrimmed(named.Label, named.Name)
		}
	case source.Kind == triggerCallerCrew:
		agent = "Agent"
		if source.ChatKey != "" {
			if identity, err := defaultProductConversationRegistryStore().agentIdentity(ctx, source.UserID, source.Profile, source.ChatKey); err == nil {
				agent = firstNonEmptyTrimmed(identity.Name, agent)
			}
		}
	case source.Kind == triggerCallerWorkflow:
		agent = source.Label
	}
	agent = agentMessageReadableName(agent)
	person := agentMessageReadableName(shareOwnerLabel(source.UserID))
	if account, _, ok := macSenderAccount(source); ok && (person == "Agent" || person == "unknown") {
		person = agentMessageReadableName(account.Username)
	}
	if person == "Agent" || person == "unknown" {
		person = "Person"
	}
	return person + " · " + agent, agent, channel
}

// A removed/unnamed sender stays an agent, never an opaque routing identifier
// or a transport/connection label in a person's chat bar.
func agentMessageReadableName(name string) string {
	name = cleanAgentLabel(name)
	lower := strings.ToLower(name)
	if name == "" || lower == "mcp" || lower == "agentbridge" || strings.Contains(lower, "external connection") || len(lower) > 30 && (strings.HasPrefix(lower, "app-") || strings.HasPrefix(lower, "agent-") || strings.HasPrefix(lower, "team-agent-")) || strings.HasPrefix(lower, "network:") {
		return "Agent"
	}
	return name
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

// An unnamed default agent is called Agent, as in the chat UI. Keep a custom agent name intact.
func crewMessageSenderName(crew, name string) string {
	crew, name = strings.TrimSpace(crew), strings.TrimSpace(name)
	if name == "" || name == "Builder" {
		name = "Agent"
	}
	if crew == "" || crew == name {
		return name
	}
	return crew + " · " + name
}
