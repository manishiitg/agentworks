package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestDecorateSharedBuilderHistoryShowsAuthorAndProtectsResume(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner-1","username":"manish","admin":true,"can_create":true,"products":[]},{"id":"member-1","username":"laxmi","can_create":false,"can_edit":true,"products":["agentworks"]}]}`)

	sessions := []ChatHistorySession{
		{SessionID: "mine", UserID: "member-1"},
		{SessionID: "theirs", UserID: "owner-1"},
		{SessionID: "legacy", UserID: "default"},
	}
	decorateChatHistorySessions(sessions, "member-1", false, true)

	if sessions[0].Username != "laxmi" || !sessions[0].CanResume || !sessions[0].CanDelete {
		t.Fatalf("own session decoration = %#v", sessions[0])
	}
	if sessions[1].Username != "manish" || sessions[1].CanResume || sessions[1].CanDelete {
		t.Fatalf("other collaborator session decoration = %#v", sessions[1])
	}
	if sessions[2].Username != "System / legacy" || sessions[2].CanResume || sessions[2].CanDelete {
		t.Fatalf("legacy session decoration = %#v", sessions[2])
	}

	decorateChatHistorySessions(sessions, "admin-1", true, true)
	if sessions[1].CanResume || sessions[1].CanDelete || sessions[2].CanResume || sessions[2].CanDelete {
		t.Fatalf("platform admin must not inherit private chat access: other=%#v legacy=%#v", sessions[1], sessions[2])
	}
}

func TestVisibleSharedBuilderHistoryIsPrivateEvenForPlatformAdmin(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	sessions := []ChatHistorySession{
		{SessionID: "mine", UserID: "member-1"},
		{SessionID: "theirs", UserID: "member-2"},
		{SessionID: "legacy", UserID: "default"},
	}

	memberView := visibleChatHistorySessions(sessions, "member-1", false)
	if len(memberView) != 1 || memberView[0].SessionID != "mine" {
		t.Fatalf("member view = %#v, want only own chat", memberView)
	}
	readerView := visibleChatHistorySessions(sessions, "member-2", false)
	if len(readerView) != 1 || readerView[0].SessionID != "theirs" {
		t.Fatalf("reader view = %#v, want only own chat", readerView)
	}
	workflowOwnerView := visibleChatHistorySessions(sessions, "owner-1", false)
	if len(workflowOwnerView) != 0 {
		t.Fatalf("workflow owner saw %d other-user chats, want none", len(workflowOwnerView))
	}
	adminView := visibleChatHistorySessions(sessions, "admin-1", true)
	if len(adminView) != 0 {
		t.Fatalf("admin saw %d other-user chats, want none", len(adminView))
	}
}

func TestVisibleSharedBuilderHistoryAllowsDefaultOwnerInSingleUserMode(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	sessions := []ChatHistorySession{
		{SessionID: "local", UserID: "default"},
		{SessionID: "other", UserID: "member-2"},
	}

	visible := visibleChatHistorySessions(sessions, "default", false)
	if len(visible) != 1 || visible[0].SessionID != "local" {
		t.Fatalf("single-user view = %#v, want local default-owner chat", visible)
	}
	if !chatHistoryCanResume("default", "default", false) {
		t.Fatal("single-user default owner must be able to resume its chat")
	}

	t.Setenv("MULTI_USER_MODE", "true")
	if chatHistoryCanResume("default", "default", false) {
		t.Fatal("multi-user legacy/default chat must not be resumable")
	}
}

func TestParseWorkflowBuilderHistoryUsesPersistedAuthor(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"author-1","username":"yoav","can_create":true,"products":["agentworks"]}]}`)

	session, ok := parseLocalChatHistorySession(
		"viewer-2",
		"Workflow/demo",
		"Workflow/demo",
		"session-1",
		`{"session_id":"session-1","user_id":"author-1","username":"old-name","conversation_history":[],"updated_at":"2026-09-08T10:00:00Z"}`,
		time.Time{},
	)
	if !ok {
		t.Fatal("expected workflow Builder transcript to parse")
	}
	if session.UserID != "author-1" || session.Username != "yoav" {
		t.Fatalf("author = %q/%q, want author-1/yoav", session.UserID, session.Username)
	}

	legacy, ok := parseLocalChatHistorySession(
		"viewer-2",
		"Workflow/demo",
		"Workflow/demo",
		"legacy-1",
		`{"session_id":"legacy-1","conversation_history":[],"updated_at":"2026-09-08T10:00:00Z"}`,
		time.Time{},
	)
	if !ok || legacy.UserID != "default" || legacy.Username != "System / legacy" {
		t.Fatalf("legacy author = %#v, want stable legacy attribution", legacy)
	}
}

func TestWorkflowPathFromBuilderConversation(t *testing.T) {
	path := "Workflow/demo/builder/conversation/2026-09-08/session-abc-conversation.json"
	if got := workflowPathFromBuilderConversation(path); got != "Workflow/demo" {
		t.Fatalf("workflow path = %q", got)
	}
	if got := workflowPathFromBuilderConversation("_users/alice/chat_history/session/conversation.json"); got != "" {
		t.Fatalf("personal path must not resolve as shared workflow: %q", got)
	}
}

// PLAT-872: an existing Builder saved tmux for Codex, causing resume to
// prelaunch a terminal even though the installed adapter now uses app-server.
func TestRestoredRuntimeTransportIgnoresObsoleteTmuxMetadata(t *testing.T) {
	for _, tc := range []struct {
		provider  string
		transport string
		tmux      bool
	}{
		{"codex-cli", "app-server", false},
		{"pi-cli", "rpc", false},
		{"cursor-cli", "acp", false},
		{"claude-code", "tmux", true},
		{"unknown-cli", "", false},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			for _, handleOnly := range []bool{false, true} {
				runtime := &ChatHistoryAgentRuntime{
					Kind: "coding_agent", Provider: tc.provider, Transport: "tmux",
					AgentSessionHandle: &mcpagent.AgentSessionHandle{
						Provider: llmtypes.CodingProviderSessionHandle{
							Provider: tc.provider, Transport: llmtypes.CodingProviderTransportTmux,
							NativeSessionID: "native-thread-1", TmuxSession: "saved-pane",
						},
					},
				}
				if handleOnly {
					runtime.Provider = ""
				}
				if got := restoredRuntimeCodingAgentTransport(runtime); got != tc.transport {
					t.Fatalf("handleOnly=%v transport=%q, want %q", handleOnly, got, tc.transport)
				}
				if restoredRuntimeUsesLaunchableTerminalTransport(runtime) != tc.tmux {
					t.Fatal("terminal prelaunch does not match current provider")
				}
				pane, ok, reason := restoredRuntimeTmuxSession(runtime)
				if tc.tmux {
					if !ok || pane != "saved-pane" || reason != "" {
						t.Fatalf("valid terminal refused: %q %v %q", pane, ok, reason)
					}
				} else if ok || reason != "not_tmux_transport" {
					t.Fatalf("obsolete terminal accepted: %q %v %q", pane, ok, reason)
				}
				if runtime.AgentSessionHandle.Provider.NativeSessionID != "native-thread-1" {
					t.Fatal("native continuation identity was lost")
				}
			}
		})
	}
}

func TestRestorePersistedTerminalSnapshotCreatesStaticTerminal(t *testing.T) {
	api := &StreamingAPI{terminalStore: terminals.NewStore()}
	runtime := &ChatHistoryAgentRuntime{
		Kind:          "coding_agent",
		Provider:      "codex-cli",
		WorkspacePath: "Workflow/demo",
	}
	snapshots := []terminals.Snapshot{{
		TerminalID:    "old-session:main:old-session",
		SessionID:     "old-session",
		OwnerID:       "main:old-session",
		ExecutionKind: "main_agent",
		StepTransport: "tmux",
		TmuxSession:   "dead-tmux-session",
		ContentSource: "tmux_pipe",
		Content:       "\x1b[32mwhat we did last\x1b[0m",
	}}

	terminal, started, reason := api.restorePersistedTerminalSnapshot(context.Background(), "new-session", runtime, snapshots)
	if !started || terminal == nil || reason != "" {
		t.Fatalf("restorePersistedTerminalSnapshot started=%v terminal=%#v reason=%q", started, terminal, reason)
	}
	if terminal.SessionID != "new-session" || terminal.TmuxSession != "" {
		t.Fatalf("restored terminal session/tmux = %q/%q", terminal.SessionID, terminal.TmuxSession)
	}
	if terminal.State != "stale" || terminal.Active {
		t.Fatalf("restored terminal lifecycle = active:%v state:%q", terminal.Active, terminal.State)
	}
	if terminal.ContentSource != "tmux_pipe" || !strings.Contains(terminal.Content, "\x1b[32m") {
		t.Fatalf("restored terminal lost ANSI/source: source=%q content=%q", terminal.ContentSource, terminal.Content)
	}
	stored, ok := api.terminalStore.Get("new-session:main:new-session")
	if !ok {
		t.Fatalf("expected static terminal in store")
	}
	if stored.TmuxSession != "" || stored.ContentSource != "tmux_pipe" {
		t.Fatalf("stored terminal tmux/source = %q/%q", stored.TmuxSession, stored.ContentSource)
	}
}

func TestWorkflowBotHistoryVisibleButCannotResume(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	sessions := []ChatHistorySession{
		{SessionID: "bot-slack--session", UserID: "bot-slack-8d3dd8b8e39a5d46", BotPlatform: "slack"},
		{SessionID: "private", UserID: "other-human"},
		{SessionID: "bot-slack--fake", UserID: "other-human", BotPlatform: "slack"},
	}
	got := visibleChatHistorySessions(sessions, "viewer", false)
	if len(got) != 1 || got[0].SessionID != sessions[0].SessionID {
		t.Fatalf("bot history visibility = %#v", got)
	}
	decorateChatHistorySessions(got, "viewer", false, true)
	if got[0].CanResume || got[0].CanDelete || chatHistoryCanResume(got[0].UserID, "viewer", false) {
		t.Fatal("workflow bot history must be view-only")
	}
}
