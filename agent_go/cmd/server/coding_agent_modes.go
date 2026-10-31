package server

import (
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"

	"github.com/manishiitg/mcpagent/llm"
)

func codingAgentPersistentInteractiveFlags(provider string, allowPersistentInteractive, usesStructuredTransport bool) (claudeCode bool, codexCLI bool, cursorCLI bool, piCLI bool, museCLI bool, agyCLI bool) {
	normalizedProvider := strings.ToLower(strings.TrimSpace(provider))
	if usesStructuredTransport {
		return false, false, false, false, false, false
	}
	contract, ok := llm.GetCodingAgentProviderContract(llm.Provider(normalizedProvider), "")
	if !allowPersistentInteractive || !ok || !contract.UsesPersistentSession {
		return false, false, false, false, false, false
	}

	switch normalizedProvider {
	case strings.ToLower(string(llm.ProviderClaudeCode)):
		return true, false, false, false, false, false
	case strings.ToLower(string(llm.ProviderCodexCLI)):
		return false, true, false, false, false, false
	case strings.ToLower(string(llm.ProviderCursorCLI)):
		return false, false, true, false, false, false
	case strings.ToLower(string(llm.ProviderPiCLI)):
		return false, false, false, true, false, false
	case strings.ToLower(string(llm.ProviderMuseCLI)):
		return false, false, false, false, true, false
	case strings.ToLower(string(llm.ProviderAgyCLI)):
		return false, false, false, false, false, true
	default:
		return false, false, false, false, false, false
	}
}

// codingAgentUsesStructuredTransportForChat applies AgentWorks' shared
// transport rule: chat turns retain the provider process (Codex app-server or
// a tmux CLI), whether a
// person, a schedule, a webhook, a bot or a background completion started it.
// Only workflow steps (applyWorkflowTransportToAgentConfig), delegated
// sub-agents and typed runtime stages use structured JSON. A product profile
// may choose tools, skills and models, but not a second transport policy.
func codingAgentUsesStructuredTransportForChat(provider string, retainsSession bool) bool {
	if _, ok := llm.GetCodingAgentProviderContract(llm.Provider(strings.TrimSpace(provider)), ""); !ok {
		return false
	}
	return !retainsSession
}

func codingAgentRequestAllowsPersistentInteractive(req *QueryRequest) bool {
	if req == nil {
		return false
	}
	return strings.TrimSpace(req.SessionKind) == ""
}

// codingAgentTurnAttended reports whether this turn runs in a website chat tab,
// where a coding agent's native questions, plan approvals and command approvals
// show as cards and wait for the person (owner, 2026-10-28: "all tabs on
// website... except schedules/whatsapp/slack etc need to have this decision
// thing"). The server decides from the turn's origin, not from a browser header:
// a person's message, a queued or steered follow-up and a message from another
// agent delivered into the tab are attended, whoever started the turn.
// Schedules, webhooks, Slack, WhatsApp, MCP and API callers, child sessions and
// background completions have nobody at a card, so their questions are
// auto-answered and never hold the run.
func codingAgentTurnAttended(claims *UserClaims, req *QueryRequest, sessionID string) bool {
	if req == nil {
		return false
	}
	if strings.TrimSpace(req.ParentSessionID) != "" || strings.TrimSpace(req.SessionKind) != "" {
		return false
	}
	trigger := strings.ToLower(strings.TrimSpace(req.TriggeredBy))
	if strings.TrimSpace(req.BotPlatform) != "" || strings.HasPrefix(trigger, "bot:") {
		return false
	}
	// MCP and API callers come first: the fields below are plain request JSON they could set.
	if strings.TrimSpace(req.ExternalBuilderOperationID) != "" ||
		claims != nil && (claims.AccessToken != nil || claims.ExecutionPrincipal != nil || claims.BotRouteGrant != "" || claims.ExternalBuilderOperationID != "") {
		return false
	}
	// "Make interactive" hands a schedule session to the person on purpose.
	if req.UserInteractiveContinuation {
		return true
	}
	if req.KeepNativeSessionAlive || isScheduledSessionIdentity(sessionID, req.TriggeredBy) {
		return false
	}
	// Another agent's message delivered into a tab (agent_messaging.go) runs as the tab's owner.
	if strings.TrimSpace(req.AgentMessageInbox) != "" {
		return true
	}
	if req.IsAutoNotification {
		return false
	}
	// The website sends no trigger; "interactive" marks its queued and live follow-ups. Every other trigger
	// (manual schedule runs, external, chat_tool, webhook, cron, product jobs) auto-answers.
	return trigger == "" || trigger == "interactive"
}

func codingAgentClaudeCodeChatTransport(provider string) string {
	if strings.ToLower(strings.TrimSpace(provider)) == strings.ToLower(string(llm.ProviderClaudeCode)) {
		return "" // Let the Claude-only environment setting choose tmux (default) or sdk.
	}
	return ""
}

func codingAgentWorkspaceWorkingDir(workspaceRelativeFolder string) string {
	rel := strings.TrimSpace(workspaceRelativeFolder)
	if roots, bound, err := crewStorageRoots(rel); bound && err == nil {
		if rel == roots.Virtual {
			return roots.Files
		}
		if resolved, err := fsutil.WorkspacePath(fsutil.WorkspaceDocsRoot(), rel); err == nil {
			return resolved
		}
	}
	if rel == "" {
		rel = perUserChatsFolderFor("")
	}
	resolved, err := fsutil.WorkspacePath(fsutil.WorkspaceDocsRoot(), rel)
	if err != nil {
		return ""
	}
	return resolved
}
