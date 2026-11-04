package server

import (
	"context"
	"log"
	"strings"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"

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

// codingAgentClaudeCodeChatTransport is the transport a chat's Claude Code runs on: the chat's own choice, else
// "" for the server's setting (the Agent SDK by default).
// chatClaudeCodeTransport is the chat's own Claude choice, read from its saved tab: a turn reaches the query
// path through JSON from several places (the chat route, schedules, agent messages, WhatsApp), so a value
// carried on the request alone was lost and the chat ran on the SDK while its view showed the terminal.
func chatClaudeCodeTransport(ctx context.Context, owner, sessionID, carried string) string {
	for _, candidate := range []string{owner, productWorkspaceUserID(ctx)} {
		if tab, found := chatContextTabForSession(ctx, candidate, sessionID); found {
			transport := llmproviders.ClaudeCodeTransportSDK
			if tab.Identity.ClaudeTerminal {
				transport = llmproviders.ClaudeCodeTransportTmux
			}
			log.Printf("[CLAUDE_TERMINAL] session=%s owner=%s action=route transport=%s source=chat", sessionID, candidate, transport)
			return transport
		}
	}
	log.Printf("[CLAUDE_TERMINAL] session=%s owner=%s action=route transport=%q source=request reason=no_saved_chat", sessionID, owner, carried)
	return carried
}

func codingAgentClaudeCodeChatTransport(provider, chosen string) string {
	if strings.EqualFold(strings.TrimSpace(provider), string(llm.ProviderClaudeCode)) {
		return strings.TrimSpace(chosen)
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

// liveCodingProviderTransport is the transport of the coding CLI running for a chat now ("tmux", "sdk", ...);
// empty when none runs.
func liveCodingProviderTransport(sessionID string) string {
	session, ok := mcpagent.LookupSession(sessionID)
	if !ok {
		return ""
	}
	handle := session.Snapshot()
	if handle == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(string(handle.Provider.Transport)))
}
