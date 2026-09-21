package server

import (
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"

	"github.com/manishiitg/mcpagent/llm"
)

func codingAgentPersistentInteractiveFlags(provider string, allowPersistentInteractive, usesStructuredTransport bool) (claudeCode bool, codexCLI bool, cursorCLI bool, piCLI bool, museCLI bool, agyCLI bool) {
	normalizedProvider := strings.ToLower(strings.TrimSpace(provider))
	if usesStructuredTransport {
		return false, false, false, false, false, false
	}
	if !allowPersistentInteractive ||
		!llm.IsTmuxCodingAgentProvider(llm.Provider(normalizedProvider), "") {
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
// transport rule: every chat-level coding-agent turn runs in tmux, whether a
// person, a schedule, a webhook, a bot or a background completion started it.
// Only workflow steps (applyWorkflowTransportToAgentConfig), delegated
// sub-agents and typed runtime stages use structured JSON. A product profile
// may choose tools, skills and models, but not a second transport policy.
func codingAgentUsesStructuredTransportForChat(provider string, retainsTmux bool) bool {
	if _, ok := llm.GetCodingAgentProviderContract(llm.Provider(strings.TrimSpace(provider)), ""); !ok {
		return false
	}
	return !retainsTmux
}

// codingAgentRequestAllowsPersistentInteractive reports whether a chat-level
// turn keeps its coding CLI in a retained tmux session. All of them do except
// typed runtime stages (e.g. Pulse reviewers), which run like workflow stages.
//
// Schedules, webhooks, bot turns and background completions used to fall back
// to structured JSON. When such a turn landed in a person's retained chat (a
// crew schedule posting its "Daily wrap-up" into the project conversation) it
// replaced the chat's tmux agent with a structured one; the person's later
// messages were queued for a turn that never came (RTS 2026-09-25). Retained
// panes that go idle are closed by the idle reaper.
func codingAgentRequestAllowsPersistentInteractive(req *QueryRequest) bool {
	if req == nil {
		return false
	}
	return strings.TrimSpace(req.SessionKind) == ""
}

func codingAgentClaudeCodeChatTransport(provider string) string {
	if strings.ToLower(strings.TrimSpace(provider)) == strings.ToLower(string(llm.ProviderClaudeCode)) {
		return llm.ClaudeCodeTransportTmux
	}
	return ""
}

func codingAgentWorkspaceWorkingDir(workspaceRelativeFolder string) string {
	rel := strings.TrimSpace(workspaceRelativeFolder)
	if rel == "" {
		rel = perUserChatsFolderFor("")
	}
	return filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(rel))
}
