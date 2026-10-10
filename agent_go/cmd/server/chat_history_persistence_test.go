package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestCaptureChatHistoryTerminalSnapshotsPersistsNonTmuxMultiAgentPane covers the
// multi-agent / Chief-of-Staff case: the coding agent streams its terminal over
// a non-tmux transport, so the store snapshot has no tmux_session. Persistence
// must still capture it so the pane can be restored after a server restart.
func TestCaptureChatHistoryTerminalSnapshotsPersistsNonTmuxMultiAgentPane(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "chat-non-tmux-multiagent"

	now := time.Now()
	store.HandleEvent(sessionID, internalevents.Event{
		Type:          "streaming_chunk",
		Timestamp:     now,
		SessionID:     sessionID,
		ExecutionKind: "main_agent",
		Data: &agentevents.AgentEvent{
			Type:      agentevents.StreamingChunk,
			Timestamp: now,
			SessionID: sessionID,
			Data: &agentevents.StreamingChunkEvent{
				BaseEventData: agentevents.BaseEventData{
					Timestamp: now,
					SessionID: sessionID,
					Metadata: map[string]interface{}{
						"kind":           "terminal",
						"provider":       "claude-code",
						"execution_kind": "main_agent",
						"scope":          "main_agent",
						// Intentionally no tmux_session / step_transport — the
						// non-tmux transport the bug was reported against.
					},
				},
				Content:    "\x1b[32mchief of staff output\x1b[0m\n",
				ChunkIndex: 0,
			},
		},
	})

	// Sanity: the live store snapshot is non-tmux (the condition the old
	// persist filter dropped).
	stored := store.List(sessionID)
	if len(stored) != 1 {
		t.Fatalf("store snapshot count = %d, want 1", len(stored))
	}
	if chatHistoryTerminalSnapshotIsTmux(stored[0]) {
		t.Fatalf("expected non-tmux store snapshot, got transport=%q source=%q tmux=%q", stored[0].StepTransport, stored[0].ContentSource, stored[0].TmuxSession)
	}

	snapshots := api.captureChatHistoryTerminalSnapshots(sessionID, nil)
	if len(snapshots) != 1 {
		t.Fatalf("persisted snapshot count = %d, want 1 (non-tmux multi-agent pane must persist)", len(snapshots))
	}
	if !strings.Contains(snapshots[0].Content, "chief of staff output") {
		t.Fatalf("persisted snapshot content = %q, want chief of staff output", snapshots[0].Content)
	}
}

func TestCleanChatHistoryForPersistenceRemovesRestoredConversationContext(t *testing.T) {
	originalText := "check what we did here\n\nPrevious workflow-builder conversation file: Workflow/instagram/builder/conversation/2026-05-14/session-old-conversation.json\nThis hidden context should not persist."
	history := []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: originalText},
			},
		},
		{
			Role: llmtypes.ChatMessageTypeAI,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "summary"},
			},
		},
	}

	cleaned := cleanChatHistoryForPersistence(history)

	got := cleaned[0].Parts[0].(llmtypes.TextContent).Text
	if got != "check what we did here" {
		t.Fatalf("expected restored context removed, got %q", got)
	}
	if history[0].Parts[0].(llmtypes.TextContent).Text != originalText {
		t.Fatalf("expected original history to remain unchanged")
	}
}

func TestAppendRestoredConversationContextAddsFallbackAndCleans(t *testing.T) {
	withContext := appendRestoredConversationContext("continue this", "_users/default/chat_history/2026-05-18/session/conversation.json")

	if !strings.Contains(withContext, "\n\nPrevious conversation file: _users/default/chat_history/2026-05-18/session/conversation.json") {
		t.Fatalf("expected fallback conversation file context, got %q", withContext)
	}
	if got := cleanChatHistoryQuery(withContext); got != "continue this" {
		t.Fatalf("expected restored context to clean back to user query, got %q", got)
	}
	if got := appendRestoredConversationContext(withContext, "_users/default/chat_history/other/conversation.json"); got != withContext {
		t.Fatalf("expected existing restored context to remain unchanged")
	}
}

func TestCaptureChatHistoryTerminalSnapshotsRedactsAndPreservesAnsi(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "chat-terminal-snapshot"
	_, ok := store.UpsertStaticSnapshot(sessionID, terminals.Snapshot{
		OwnerID:       "main:" + sessionID,
		ExecutionKind: "main_agent",
		StepTransport: "tmux",
		ContentSource: "tmux_pipe",
		TmuxSession:   "old-live-pane",
		Content:       "\x1b[31mred output\x1b[0m\nMCP_API_TOKEN=super-secret\nSECRET_FOO=hidden-secret\n",
		UpdatedAt:     time.Now(),
	})
	if !ok {
		t.Fatal("expected terminal snapshot to be stored")
	}

	snapshots := api.captureChatHistoryTerminalSnapshots(sessionID, nil)
	if len(snapshots) != 1 {
		t.Fatalf("terminal snapshot count = %d, want 1", len(snapshots))
	}
	got := snapshots[0]
	if got.ContentSource != "tmux_pipe" {
		t.Fatalf("content source = %q, want tmux_pipe", got.ContentSource)
	}
	if !strings.Contains(got.Content, "\x1b[31m") || !strings.Contains(got.Content, "red output") {
		t.Fatalf("expected ANSI terminal content to be preserved, got %q", got.Content)
	}
	for _, leaked := range []string{"super-secret", "hidden-secret"} {
		if strings.Contains(got.Content, leaked) {
			t.Fatalf("terminal snapshot leaked secret %q: %q", leaked, got.Content)
		}
	}
	for _, redacted := range []string{"MCP_API_TOKEN=[redacted]", "SECRET_FOO=[redacted]"} {
		if !strings.Contains(got.Content, redacted) {
			t.Fatalf("terminal snapshot missing redacted marker %q: %q", redacted, got.Content)
		}
	}
	if got.Active || got.State != "stale" || len(got.Rows) != 0 {
		t.Fatalf("persisted snapshot lifecycle = active:%v state:%q rows:%d", got.Active, got.State, len(got.Rows))
	}
}

func TestCaptureChatHistoryTerminalSnapshotsPrefersStoredTmuxStream(t *testing.T) {
	captureCalled := false
	oldCapture := captureChatHistoryTerminalPaneLines
	captureChatHistoryTerminalPaneLines = func(_ context.Context, tmuxSession string, lines int) (string, error) {
		captureCalled = true
		if tmuxSession != "live-runtime-pane" {
			t.Fatalf("tmux session = %q, want live-runtime-pane", tmuxSession)
		}
		if lines != terminalDefaultDetailHistoryLines {
			t.Fatalf("capture lines = %d, want %d", lines, terminalDefaultDetailHistoryLines)
		}
		return "\x1b[32mruntime output\x1b[0m\nMCP_API_TOKEN=super-secret\n", nil
	}
	defer func() { captureChatHistoryTerminalPaneLines = oldCapture }()

	sessionID := "chat-runtime-terminal"
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	terminalID := sessionID + ":main:" + sessionID
	store.HandleEvent(sessionID, terminalRouteChunkEvent(
		sessionID,
		"main:"+sessionID,
		"live-runtime-pane",
		"initial screen",
		1,
	))
	if _, ok := store.SetDisplayContent(
		terminalID,
		"\x1bcfirst raw line\r\nsecond raw line\r\nfinal raw line\n",
		"tmux_stream",
	); !ok {
		t.Fatal("expected live tmux stream to be stored")
	}
	runtime := &ChatHistoryAgentRuntime{
		Kind:          "coding_agent",
		Provider:      "codex-cli",
		Transport:     llmtypes.CodingProviderTransportTmux,
		WorkspacePath: "Workflow/substack",
		AgentSessionHandle: &mcpagent.AgentSessionHandle{
			SessionID: sessionID,
			Provider: llmtypes.CodingProviderSessionHandle{
				Provider:    "codex-cli",
				Transport:   llmtypes.CodingProviderTransportTmux,
				TmuxSession: "live-runtime-pane",
			},
		},
	}

	snapshots := api.captureChatHistoryTerminalSnapshots(sessionID, runtime)
	if len(snapshots) != 1 {
		t.Fatalf("terminal snapshot count = %d, want 1", len(snapshots))
	}
	got := snapshots[0]
	if got.TmuxSession != "live-runtime-pane" || got.ContentSource != "tmux_stream" {
		t.Fatalf("runtime fallback metadata = transport:%q tmux:%q source:%q", got.StepTransport, got.TmuxSession, got.ContentSource)
	}
	if captureCalled {
		t.Fatal("capture-pane fallback ran even though a complete tmux stream was stored")
	}
	for _, line := range []string{"first raw line", "second raw line", "final raw line"} {
		if !strings.Contains(got.Content, line) {
			t.Fatalf("stored stream lost %q: %q", line, got.Content)
		}
	}
}

func TestReadChatHistoryTerminalSnapshotsFromPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	convDir := filepath.Join(root, "_users", "default", "chat_history", "2026-05-18")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("mkdir conversation dir: %v", err)
	}
	convPath := filepath.Join(convDir, "session-chat-1-conversation.json")
	data := `{
  "session_id": "chat-1",
  "conversation_history": [],
  "terminal_snapshots": [
    {
      "terminal_id": "chat-1:main:chat-1",
      "session_id": "chat-1",
      "owner_id": "main:chat-1",
      "execution_kind": "main_agent",
      "step_transport": "tmux",
      "content_source": "tmux_pipe",
      "content": "\u001b[34mblue output\u001b[0m\nMCP_API_TOKEN=super-secret",
      "state": "running"
    }
  ]
}`
	if err := os.WriteFile(convPath, []byte(data), 0o600); err != nil {
		t.Fatalf("write conversation: %v", err)
	}

	snapshots, ok, err := ReadChatHistoryTerminalSnapshotsFromPath("default", "_users/default/chat_history/2026-05-18/session-chat-1-conversation.json")
	if err != nil {
		t.Fatalf("read terminal snapshots error = %v", err)
	}
	if !ok || len(snapshots) != 1 {
		t.Fatalf("snapshots ok=%v len=%d, want ok with 1", ok, len(snapshots))
	}
	got := snapshots[0]
	if got.ContentSource != "tmux_pipe" || !strings.Contains(got.Content, "\x1b[34m") {
		t.Fatalf("snapshot source/content = %q/%q", got.ContentSource, got.Content)
	}
	if strings.Contains(got.Content, "super-secret") || !strings.Contains(got.Content, "MCP_API_TOKEN=[redacted]") {
		t.Fatalf("snapshot was not redacted: %q", got.Content)
	}
	if got.Active || got.State != "stale" {
		t.Fatalf("snapshot lifecycle = active:%v state:%q", got.Active, got.State)
	}
}

func TestReadChatHistoryTerminalSnapshotsFallsBackToUIEvents(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	convDir := filepath.Join(root, "_users", "default", "chat_history", "2026-05-18")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("mkdir conversation dir: %v", err)
	}
	convPath := filepath.Join(convDir, "session-legacy-codex-conversation.json")
	data := `{
  "session_id": "legacy-codex",
  "runtime": {
    "kind": "coding_agent",
    "provider": "codex-cli",
    "transport": "tmux",
    "workspace_path": "Workflow/substack"
  },
  "conversation_history": [],
  "ui_events": [
    {
      "type": "streaming_chunk",
      "timestamp": "2026-06-13T12:00:00Z",
      "session_id": "legacy-codex",
      "execution_id": "main:legacy-codex",
      "execution_kind": "main_agent",
      "data": {
        "type": "streaming_chunk",
        "timestamp": "2026-06-13T12:00:00Z",
        "session_id": "legacy-codex",
        "data": {
          "timestamp": "2026-06-13T12:00:00Z",
          "session_id": "legacy-codex",
          "content": "\u001b[35mlegacy pane\u001b[0m\nSECRET_FOO=hidden-secret\n",
          "chunk_index": 7,
          "metadata": {
            "kind": "terminal",
            "provider": "codex-cli",
            "tmux_session": "legacy-pane"
          }
        }
      }
    }
  ]
}`
	if err := os.WriteFile(convPath, []byte(data), 0o600); err != nil {
		t.Fatalf("write conversation: %v", err)
	}

	snapshots, ok, err := ReadChatHistoryTerminalSnapshotsFromPath("default", "_users/default/chat_history/2026-05-18/session-legacy-codex-conversation.json")
	if err != nil {
		t.Fatalf("read terminal snapshots error = %v", err)
	}
	if !ok || len(snapshots) != 1 {
		t.Fatalf("snapshots ok=%v len=%d, want ok with 1", ok, len(snapshots))
	}
	got := snapshots[0]
	if got.TmuxSession != "legacy-pane" || got.StepTransport != "tmux" || got.ContentSource != "tmux_capture" {
		t.Fatalf("legacy snapshot metadata = tmux:%q transport:%q source:%q", got.TmuxSession, got.StepTransport, got.ContentSource)
	}
	if got.WorkflowPath != "Workflow/substack" || got.ChunkIndex != 7 {
		t.Fatalf("legacy snapshot workflow/chunk = %q/%d", got.WorkflowPath, got.ChunkIndex)
	}
	if !strings.Contains(got.Content, "\x1b[35m") || !strings.Contains(got.Content, "legacy pane") {
		t.Fatalf("expected ANSI legacy terminal content, got %q", got.Content)
	}
	if strings.Contains(got.Content, "hidden-secret") || !strings.Contains(got.Content, "SECRET_FOO=[redacted]") {
		t.Fatalf("legacy snapshot was not redacted: %q", got.Content)
	}
}

func TestResolveRestoredCodingConversationPersistTargetIsTransportSpecific(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	convDir := filepath.Join(root, "_users", "default", "chat_history", "2026-05-18")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("mkdir conversation dir: %v", err)
	}
	codingPath := filepath.Join(convDir, "session-codex-old-conversation.json")
	codingJSON := `{
  "session_id": "codex-old",
  "conversation_history": [
    {"Role": "human", "Parts": [{"Text": "old request"}]},
    {"Role": "ai", "Parts": [{"Text": "old answer"}]}
  ],
  "runtime": {
    "kind": "coding_agent",
    "provider": "codex-cli",
    "transport": "tmux",
    "external_session_id": "codex-native-thread",
    "resume_supported": true
  }
}`
	if err := os.WriteFile(codingPath, []byte(codingJSON), 0o600); err != nil {
		t.Fatalf("write coding conversation: %v", err)
	}

	api := &StreamingAPI{}
	target, ok, err := api.resolveRestoredCodingConversationPersistTarget(
		"default",
		"new-ui-session",
		"_users/default/chat_history/2026-05-18/session-codex-old-conversation.json",
		"",
		"",
		"codex-cli",
		"",
	)
	if err != nil {
		t.Fatalf("resolve coding target error = %v", err)
	}
	if !ok || target == nil {
		t.Fatal("expected coding-agent target")
	}
	if target.SessionID != "codex-old" || target.ConversationPath != "_users/default/chat_history/2026-05-18/session-codex-old-conversation.json" {
		t.Fatalf("target = %#v", target)
	}
	merged := mergeRestoredChatHistory(target.History, []llmtypes.MessageContent{{
		Role:  llmtypes.ChatMessageTypeHuman,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "new request"}},
	}})
	if len(merged) != 3 {
		t.Fatalf("merged history length = %d, want 3", len(merged))
	}

	remembered, ok, err := api.resolveRestoredCodingConversationPersistTarget(
		"default",
		"new-ui-session",
		"",
		"",
		"",
		"codex-cli",
		"",
	)
	if err != nil {
		t.Fatalf("resolve remembered target error = %v", err)
	}
	if !ok || remembered == nil || remembered.SessionID != "codex-old" {
		t.Fatalf("expected remembered coding target, ok=%v target=%#v", ok, remembered)
	}

	normalPath := filepath.Join(convDir, "session-normal-old-conversation.json")
	normalJSON := `{
  "session_id": "normal-old",
  "conversation_history": [
    {"Role": "human", "Parts": [{"Text": "normal old request"}]}
  ],
  "runtime": {
    "kind": "llm_agent",
    "provider": "openai",
    "resume_supported": false
  }
}`
	if err := os.WriteFile(normalPath, []byte(normalJSON), 0o600); err != nil {
		t.Fatalf("write normal conversation: %v", err)
	}
	normalTarget, normalOK, err := api.resolveRestoredCodingConversationPersistTarget(
		"default",
		"normal-new-ui-session",
		"_users/default/chat_history/2026-05-18/session-normal-old-conversation.json",
		"",
		"",
		"openai",
		"",
	)
	if err != nil {
		t.Fatalf("resolve normal target error = %v", err)
	}
	if normalOK || normalTarget != nil {
		t.Fatalf("normal llm restore should not reuse old persistence target, ok=%v target=%#v", normalOK, normalTarget)
	}
}

func TestMergeNativeContinuationChatHistoryRetainsPriorTurnsAndReplacesSystemPrompt(t *testing.T) {
	message := func(role llmtypes.ChatMessageType, text string) llmtypes.MessageContent {
		return llmtypes.MessageContent{
			Role:  role,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}},
		}
	}
	existing := []llmtypes.MessageContent{
		message(llmtypes.ChatMessageTypeSystem, "old runtime prompt"),
		message(llmtypes.ChatMessageTypeHuman, "first request"),
		message(llmtypes.ChatMessageTypeAI, "first answer"),
	}
	current := []llmtypes.MessageContent{
		message(llmtypes.ChatMessageTypeSystem, "new runtime prompt"),
		// Claude/Codex may return the prior exchange cumulatively even though
		// AgentWorks did not replay it into this wrapper.
		message(llmtypes.ChatMessageTypeHuman, "first request"),
		message(llmtypes.ChatMessageTypeAI, "first answer"),
		message(llmtypes.ChatMessageTypeHuman, "second request"),
		message(llmtypes.ChatMessageTypeAI, "second answer"),
	}

	merged := mergeNativeContinuationChatHistory(existing, current)
	if len(merged) != 5 {
		t.Fatalf("merged history length = %d, want 5", len(merged))
	}
	wantRoles := []llmtypes.ChatMessageType{
		llmtypes.ChatMessageTypeSystem,
		llmtypes.ChatMessageTypeHuman,
		llmtypes.ChatMessageTypeAI,
		llmtypes.ChatMessageTypeHuman,
		llmtypes.ChatMessageTypeAI,
	}
	for index, wantRole := range wantRoles {
		if merged[index].Role != wantRole {
			t.Fatalf("merged[%d].Role = %q, want %q", index, merged[index].Role, wantRole)
		}
	}
	if got := chatHistoryPartText(merged[0].Parts[0]); got != "new runtime prompt" {
		t.Fatalf("system prompt = %q, want newest prompt", got)
	}
}

func TestMergeModeChangedChatHistoryRetainsTurnsAndDropsTransportPointer(t *testing.T) {
	message := func(role llmtypes.ChatMessageType, text string) llmtypes.MessageContent {
		return llmtypes.MessageContent{
			Role:  role,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}},
		}
	}
	previous := []llmtypes.MessageContent{
		message(llmtypes.ChatMessageTypeSystem, "Run prompt"),
		message(llmtypes.ChatMessageTypeHuman, "run the workflow"),
		message(llmtypes.ChatMessageTypeAI, "run complete"),
	}
	current := []llmtypes.MessageContent{
		message(llmtypes.ChatMessageTypeHuman, "synthetic conversation-file pointer"),
		message(llmtypes.ChatMessageTypeHuman, "start Pulse"),
		message(llmtypes.ChatMessageTypeAI, "Pulse complete"),
	}

	merged := mergeModeChangedChatHistory(previous, current)
	if len(merged) != 5 {
		t.Fatalf("merged history length = %d, want 5", len(merged))
	}
	for _, msg := range merged {
		for _, part := range msg.Parts {
			if strings.Contains(chatHistoryPartText(part), "synthetic conversation-file pointer") {
				t.Fatal("transport-only conversation pointer leaked into durable history")
			}
		}
	}
	if got := chatHistoryPartText(merged[3].Parts[0]); got != "start Pulse" {
		t.Fatalf("first post-switch user turn = %q, want start Pulse", got)
	}
	if got := mergeModeChangedChatHistory(previous, nil); len(got) != len(previous) {
		t.Fatalf("empty current history discarded prior turns: got %d want %d", len(got), len(previous))
	}
}

func TestPhaseUsageCumulativeKeySeparatesNativeRuntimeEpochs(t *testing.T) {
	first := &ChatHistoryAgentRuntime{Provider: "codex-cli", ExternalSessionID: "native-run"}
	second := &ChatHistoryAgentRuntime{Provider: "codex-cli", ExternalSessionID: "native-pulse"}
	firstKey := phaseUsageCumulativeKey("schedule-1", "turn-1", first)
	if firstKey == phaseUsageCumulativeKey("schedule-1", "turn-2", second) {
		t.Fatal("different native runtime epochs share one cumulative usage key")
	}
	if firstKey != phaseUsageCumulativeKey("schedule-1", "turn-2", first) {
		t.Fatal("same native runtime did not keep a stable cumulative usage key")
	}
	if got := phaseUsageCumulativeKey("schedule-1", "turn-1", nil); got != "schedule-1|turn:turn-1" {
		t.Fatalf("non-native turn key = %q, want schedule-1|turn:turn-1", got)
	}
	if got := phaseUsageCumulativeKey("schedule-1", "", nil); got != "schedule-1" {
		t.Fatalf("empty-turn fallback key = %q, want schedule-1", got)
	}
}

func TestPhaseUsageCountsFullFirstSnapshotAfterModeRelaunch(t *testing.T) {
	file := &orchestrator.PhaseTokenUsageFile{}
	now := time.Now()
	runRuntime := &ChatHistoryAgentRuntime{Provider: "codex-cli", ExternalSessionID: "native-run"}
	pulseRuntime := &ChatHistoryAgentRuntime{Provider: "codex-cli", ExternalSessionID: "native-pulse"}

	orchestrator.ApplyCumulativeSessionModelUsageToPhaseTokenUsageFile(
		file,
		phaseUsageCumulativeKey("schedule-1", "run-turn", runRuntime),
		"workflow-builder",
		"gpt-test",
		&orchestrator.ModelTokenUsage{InputTokens: 1000, OutputTokens: 100, LLMCallCount: 1},
		now,
	)
	pulseDelta := orchestrator.ApplyCumulativeSessionModelUsageToPhaseTokenUsageFile(
		file,
		phaseUsageCumulativeKey("schedule-1", "pulse-turn", pulseRuntime),
		"workflow-builder",
		"gpt-test",
		&orchestrator.ModelTokenUsage{InputTokens: 1500, OutputTokens: 150, LLMCallCount: 1},
		now,
	)

	if pulseDelta.InputTokens != 1500 || pulseDelta.OutputTokens != 150 {
		t.Fatalf("first Pulse snapshot after relaunch was reduced to %+v", pulseDelta)
	}
	total := file.ByModel["gpt-test"]
	if total.InputTokens != 2500 || total.OutputTokens != 250 || total.LLMCallCount != 2 {
		t.Fatalf("combined Run+Pulse usage = %+v, want 2500 input / 250 output / 2 calls", total)
	}
}

func TestModeChangeHistorySnapshotFallsBackToCanonicalConversationJSON(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	conversationPath := filepath.Join("Workflow", "testing", "builder", "conversation", "2026-09-06", "session-schedule-1-conversation.json")
	absPath := filepath.Join(root, conversationPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o700); err != nil {
		t.Fatal(err)
	}
	want := []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "scheduled run"}}},
		{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "run complete"}}},
	}
	data, err := json.Marshal(map[string]interface{}{
		"session_id":           "schedule-1",
		"conversation_history": want,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := modeChangeHistorySnapshot("default", filepath.ToSlash(conversationPath), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) || chatHistoryPartText(got[0].Parts[0]) != "scheduled run" || chatHistoryPartText(got[1].Parts[0]) != "run complete" {
		t.Fatalf("disk fallback history = %#v, want both prior turns", got)
	}
}

func TestMergeRestoredChatHistoryIgnoresChangingSystemPromptWhenBodyIsCumulative(t *testing.T) {
	message := func(role llmtypes.ChatMessageType, text string) llmtypes.MessageContent {
		return llmtypes.MessageContent{
			Role:  role,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}},
		}
	}
	existing := []llmtypes.MessageContent{
		message(llmtypes.ChatMessageTypeSystem, "prompt rendered at 11:00"),
		message(llmtypes.ChatMessageTypeHuman, "first request"),
		message(llmtypes.ChatMessageTypeAI, "first answer"),
	}
	incoming := []llmtypes.MessageContent{
		message(llmtypes.ChatMessageTypeSystem, "prompt rendered at 11:05"),
		message(llmtypes.ChatMessageTypeHuman, "first request"),
		message(llmtypes.ChatMessageTypeAI, "first answer"),
		message(llmtypes.ChatMessageTypeHuman, "second request"),
		message(llmtypes.ChatMessageTypeAI, "second answer"),
	}

	merged := mergeRestoredChatHistory(existing, incoming)
	if len(merged) != len(incoming) {
		t.Fatalf("merged history length = %d, want %d", len(merged), len(incoming))
	}
	if got := chatHistoryPartText(merged[0].Parts[0]); got != "prompt rendered at 11:05" {
		t.Fatalf("system prompt = %q, want newest prompt", got)
	}
}

func TestMergeCodingAgentFallbackHistoryDropsInjectedReplayBeforePersistence(t *testing.T) {
	message := func(role llmtypes.ChatMessageType, text string) llmtypes.MessageContent {
		return llmtypes.MessageContent{Role: role, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}}}
	}
	existing := []llmtypes.MessageContent{
		message(llmtypes.ChatMessageTypeHuman, "old request"),
		message(llmtypes.ChatMessageTypeAI, "old answer"),
		message(llmtypes.ChatMessageTypeHuman, "recent request"),
		message(llmtypes.ChatMessageTypeAI, "recent answer"),
	}
	agentHistory := []llmtypes.MessageContent{
		message(llmtypes.ChatMessageTypeSystem, "current product prompt"),
		message(llmtypes.ChatMessageTypeHuman, "[AGENTWORKS CONVERSATION CONTINUITY] internal"),
		message(llmtypes.ChatMessageTypeHuman, "recent request"),
		message(llmtypes.ChatMessageTypeAI, "recent answer"),
		message(llmtypes.ChatMessageTypeHuman, "new request"),
		message(llmtypes.ChatMessageTypeAI, "new answer"),
	}
	merged := mergeCodingAgentFallbackChatHistory(existing, agentHistory, 1, 3)
	texts := make([]string, 0, len(merged))
	for _, msg := range merged {
		if len(msg.Parts) > 0 {
			texts = append(texts, chatHistoryPartText(msg.Parts[0]))
		}
	}
	// The newest system prompt leads, once; it is never inserted mid-history.
	want := []string{"current product prompt", "old request", "old answer", "recent request", "recent answer", "new request", "new answer"}
	if len(texts) != len(want) {
		t.Fatalf("merged messages = %q, want %q", texts, want)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Fatalf("merged[%d] = %q, want %q; all=%q", i, texts[i], want[i], texts)
		}
	}
}

func TestShouldAttachRestoredConversationFallbackSkipsMatchingCodingAgent(t *testing.T) {
	runtime := &ChatHistoryAgentRuntime{
		Kind:              "coding_agent",
		Provider:          "codex-cli",
		ExternalSessionID: "codex-thread-1",
		ResumeSupported:   true,
		WorkshopMode:      "optimizer",
	}

	if shouldAttachRestoredConversationFallback(runtime, "codex-cli", "optimizer") {
		t.Fatalf("expected coding-agent CLI restore to skip visible file fallback")
	}
}

func TestShouldAttachRestoredConversationFallbackKeepsFallbackForNonCodingOrMismatch(t *testing.T) {
	cases := []struct {
		name            string
		runtime         *ChatHistoryAgentRuntime
		currentProvider string
		currentMode     string
	}{
		{
			name: "missing runtime",
		},
		{
			name: "plain llm agent",
			runtime: &ChatHistoryAgentRuntime{
				Kind:     "llm_agent",
				Provider: "openai",
			},
			currentProvider: "openai",
		},
		{
			name: "provider mismatch",
			runtime: &ChatHistoryAgentRuntime{
				Kind:              "coding_agent",
				Provider:          "codex-cli",
				ExternalSessionID: "codex-thread-1",
				ResumeSupported:   true,
				WorkshopMode:      "workshop",
			},
			currentProvider: "claude-code",
			currentMode:     "workshop",
		},
		{
			// Before the workshop merge, "builder" vs "optimizer" was a real
			// mismatch. Both now normalize to "workshop", so the only true
			// workshop-mode mismatch is workshop ↔ run.
			name: "mode mismatch",
			runtime: &ChatHistoryAgentRuntime{
				Kind:              "coding_agent",
				Provider:          "codex-cli",
				ExternalSessionID: "codex-thread-1",
				ResumeSupported:   true,
				WorkshopMode:      "workshop",
			},
			currentProvider: "codex-cli",
			currentMode:     "run",
		},
		{
			name: "coding agent missing native resume support",
			runtime: &ChatHistoryAgentRuntime{
				Kind:         "coding_agent",
				Provider:     "codex-cli",
				WorkshopMode: "workshop",
			},
			currentProvider: "codex-cli",
			currentMode:     "workshop",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !shouldAttachRestoredConversationFallback(tc.runtime, tc.currentProvider, tc.currentMode) {
				t.Fatalf("expected restored file fallback for %#v", tc.runtime)
			}
		})
	}
}

func TestCleanChatHistoryForPersistenceLeavesAssistantMessages(t *testing.T) {
	text := "assistant note\n\nPrevious workflow-builder conversation file: should remain"
	history := []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeAI,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: text},
			},
		},
	}

	cleaned := cleanChatHistoryForPersistence(history)

	got := cleaned[0].Parts[0].(llmtypes.TextContent).Text
	if got != text {
		t.Fatalf("assistant message should not be cleaned, got %q", got)
	}
}

func TestChatHistoryPreviewMessagesSkipsNoisyEntries(t *testing.T) {
	history := []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "[AUTO-NOTIFICATION] Agent completed."},
			},
		},
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "check what we did here\n\nPrevious workflow-builder conversation file: old.json"},
			},
		},
		{
			Role: llmtypes.ChatMessageTypeAI,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "[Previous tool result]: noisy"},
			},
		},
		{
			Role: llmtypes.ChatMessageTypeAI,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "We added the format library."},
			},
		},
	}

	preview := chatHistoryPreviewMessages(history)

	if len(preview) != 2 {
		t.Fatalf("expected 2 preview messages, got %d: %#v", len(preview), preview)
	}
	if preview[0].Role != "human" || preview[0].Text != "check what we did here" {
		t.Fatalf("unexpected first preview: %#v", preview[0])
	}
	if preview[1].Role != "ai" || preview[1].Text != "We added the format library." {
		t.Fatalf("unexpected second preview: %#v", preview[1])
	}
}

func TestTrimChatHistoryUIEventsKeepsMostRecentEvents(t *testing.T) {
	events := make([]internalevents.Event, maxPersistedChatHistoryUIEvents+5)
	for i := range events {
		events[i] = internalevents.Event{ID: fmt.Sprintf("event-%03d", i)}
	}

	trimmed := trimChatHistoryUIEvents(events)

	if len(trimmed) != maxPersistedChatHistoryUIEvents {
		t.Fatalf("trimmed length = %d, want %d", len(trimmed), maxPersistedChatHistoryUIEvents)
	}
	if trimmed[0].ID != "event-005" {
		t.Fatalf("first retained event = %q, want event-005", trimmed[0].ID)
	}
	if trimmed[len(trimmed)-1].ID != "event-204" {
		t.Fatalf("last retained event = %q, want event-204", trimmed[len(trimmed)-1].ID)
	}
}

func TestReadWorkflowScopedChatHistoryConversationDirect(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	workspacePath := "Workflow/demo"
	sessionID := "direct-1"
	convDir := filepath.Join(root, filepath.FromSlash(workspacePath), "builder", "conversation", "2026-05-16")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("mkdir conversation dir: %v", err)
	}
	want := []byte(`{"session_id":"direct-1","conversation_history":[]}`)
	if err := os.WriteFile(filepath.Join(convDir, "session-"+sessionID+"-conversation.json"), want, 0o600); err != nil {
		t.Fatalf("write conversation: %v", err)
	}

	got, ok, err := readWorkflowScopedChatHistoryConversationDirect(sessionID, workspacePath)
	if err != nil {
		t.Fatalf("direct read error = %v", err)
	}
	if !ok {
		t.Fatal("direct read ok = false, want true")
	}
	if string(got) != string(want) {
		t.Fatalf("direct read = %s, want %s", got, want)
	}
}

func TestListChatHistorySessionsFromDiskReadsDateBucketLayout(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	convDir := filepath.Join(root, "_users", "default", "chat_history", "2026-05-17")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("mkdir conversation dir: %v", err)
	}
	convPath := filepath.Join(convDir, "session-chat-1-conversation.json")
	data := `{
  "session_id": "chat-1",
  "agent_mode": "simple",
  "conversation_history": [
    {"Role": "human", "Parts": [{"Text": "hello from date bucket"}]}
  ],
  "updated_at": "2026-05-17T10:00:00Z"
}`
	if err := os.WriteFile(convPath, []byte(data), 0o600); err != nil {
		t.Fatalf("write conversation: %v", err)
	}

	sessions, ok, err := listChatHistorySessionsFromDisk("default", "_users/default/chat_history", "", 10, 0)
	if err != nil {
		t.Fatalf("list error = %v", err)
	}
	if !ok {
		t.Fatal("list ok = false, want true")
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions len = %d, want 1: %#v", len(sessions), sessions)
	}
	if sessions[0].SessionID != "chat-1" {
		t.Fatalf("session id = %q, want chat-1", sessions[0].SessionID)
	}
	if sessions[0].ConversationPath != "_users/default/chat_history/2026-05-17/session-chat-1-conversation.json" {
		t.Fatalf("conversation path = %q", sessions[0].ConversationPath)
	}
	if _, err := os.Stat(filepath.Join(root, "_users", "default", "chat_history", chatHistoryIndexFileName)); err != nil {
		t.Fatalf("legacy listing did not backfill chat index: %v", err)
	}
}

func TestListChatHistorySessionsFromDiskUsesIndexWithoutParsingConversation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	workspaceRoot := "_users/default/chat_history"
	workspaceConversationPath := workspaceRoot + "/2026-08-03/session-indexed-chat-conversation.json"
	convDir := filepath.Join(root, filepath.FromSlash(workspaceRoot), "2026-08-03")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("mkdir conversation dir: %v", err)
	}
	convPath := filepath.Join(convDir, "session-indexed-chat-conversation.json")
	// Deliberately invalid JSON: a passing list proves it used the metadata
	// index and did not open+parse the complete conversation.
	if err := os.WriteFile(convPath, []byte("complete transcript must not be parsed"), 0o600); err != nil {
		t.Fatalf("write sentinel conversation: %v", err)
	}
	info, err := os.Stat(convPath)
	if err != nil {
		t.Fatalf("stat sentinel conversation: %v", err)
	}

	index := newChatHistoryIndex()
	index.Complete = true
	putLocalChatHistoryIndexEntry(&index, localChatHistoryFile{
		sessionID:     "indexed-chat",
		convPath:      convPath,
		workspacePath: workspaceConversationPath,
		modTime:       info.ModTime(),
		size:          info.Size(),
	}, ChatHistorySession{
		SessionID:        "indexed-chat",
		AgentMode:        "simple",
		Status:           "completed",
		Query:            "indexed title",
		UserID:           "default",
		ConversationPath: workspaceConversationPath,
		CreatedAt:        "2026-08-03T10:00:00Z",
		UpdatedAt:        "2026-08-03T10:00:00Z",
		MessageCount:     42,
		PreviewMessages: []ChatHistoryPreviewMessage{{
			Role: "human",
			Text: "short indexed preview",
		}},
	})
	if err := writeLocalChatHistoryIndex(filepath.Join(root, filepath.FromSlash(workspaceRoot), chatHistoryIndexFileName), &index); err != nil {
		t.Fatalf("write chat index: %v", err)
	}

	sessions, ok, err := listChatHistorySessionsFromDisk("default", workspaceRoot, "", 10, 0)
	if err != nil {
		t.Fatalf("list error = %v", err)
	}
	if !ok || len(sessions) != 1 {
		t.Fatalf("list ok=%v sessions=%#v, want one indexed session", ok, sessions)
	}
	if sessions[0].Query != "indexed title" || sessions[0].MessageCount != 42 {
		t.Fatalf("indexed metadata not returned: %#v", sessions[0])
	}
}

func TestListChatHistorySessionsLegacyMigrationIndexesAllFilesBeforePagination(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	workspaceRoot := "_users/default/chat_history"
	convDir := filepath.Join(root, filepath.FromSlash(workspaceRoot), "2026-08-03")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("mkdir conversation dir: %v", err)
	}
	for i := 1; i <= 3; i++ {
		data := fmt.Sprintf(`{
  "session_id": "legacy-%d",
  "agent_mode": "simple",
  "conversation_history": [{"Role":"human","Parts":[{"Text":"legacy title %d"}]}],
  "updated_at": "2026-08-03T10:0%d:00Z"
}`, i, i, i)
		path := filepath.Join(convDir, fmt.Sprintf("session-legacy-%d-conversation.json", i))
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatalf("write legacy conversation %d: %v", i, err)
		}
	}

	sessions, ok, err := listChatHistorySessionsFromDisk("default", workspaceRoot, "", 1, 0)
	if err != nil || !ok || len(sessions) != 1 {
		t.Fatalf("first paginated list ok=%v err=%v sessions=%#v", ok, err, sessions)
	}
	index, exists := readLocalChatHistoryIndex(filepath.Join(root, filepath.FromSlash(workspaceRoot), chatHistoryIndexFileName))
	if !exists || !index.Complete || len(index.Entries) != 3 {
		t.Fatalf("migration index exists=%v complete=%v entries=%d, want true/true/3", exists, index.Complete, len(index.Entries))
	}
}

func TestListWorkflowChatHistorySessionsUsesIndexWithoutParsingConversation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	workflowPath := "Workflow/indexed-workflow"
	workspaceConversationPath := workflowPath + "/builder/conversation/2026-08-03/session-workflow-chat-conversation.json"
	convDir := filepath.Join(root, filepath.FromSlash(workflowPath), "builder", "conversation", "2026-08-03")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("mkdir conversation dir: %v", err)
	}
	convPath := filepath.Join(convDir, "session-workflow-chat-conversation.json")
	if err := os.WriteFile(convPath, []byte("complete workflow transcript must not be parsed"), 0o600); err != nil {
		t.Fatalf("write sentinel conversation: %v", err)
	}
	info, err := os.Stat(convPath)
	if err != nil {
		t.Fatalf("stat sentinel conversation: %v", err)
	}

	index := newChatHistoryIndex()
	index.Complete = true
	putLocalChatHistoryIndexEntry(&index, localChatHistoryFile{
		sessionID:     "workflow-chat",
		convPath:      convPath,
		workspacePath: workspaceConversationPath,
		modTime:       info.ModTime(),
		size:          info.Size(),
	}, ChatHistorySession{
		SessionID:        "workflow-chat",
		AgentMode:        "workflow",
		Status:           "completed",
		Query:            "workflow indexed title",
		UserID:           "default",
		WorkspacePath:    workflowPath,
		ConversationPath: workspaceConversationPath,
		CreatedAt:        "2026-08-03T11:00:00Z",
		UpdatedAt:        "2026-08-03T11:00:00Z",
		MessageCount:     73,
	})
	indexPath := filepath.Join(root, filepath.FromSlash(workflowPath), "builder", "conversation", chatHistoryIndexFileName)
	if err := writeLocalChatHistoryIndex(indexPath, &index); err != nil {
		t.Fatalf("write workflow chat index: %v", err)
	}

	sessions, err := ListChatHistorySessions("default", 10, 0, workflowPath)
	if err != nil {
		t.Fatalf("list workflow sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Query != "workflow indexed title" || sessions[0].MessageCount != 73 {
		t.Fatalf("workflow indexed metadata not returned: %#v", sessions)
	}
}

func TestListWorkspaceChatHistoryIncludesCentralProductConversations(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	workspacePath := "Chats/Work/projects/demo-12345678"
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(workspacePath)), 0o755); err != nil {
		t.Fatalf("mkdir product workspace: %v", err)
	}
	conversationDir := filepath.Join(root, "_users", "default", "chat_history", "2026-09-14")
	if err := os.MkdirAll(conversationDir, 0o755); err != nil {
		t.Fatalf("mkdir chat history: %v", err)
	}
	writeProductChat := func(sessionID, runtimeWorkspace, updatedAt string) {
		t.Helper()
		data := fmt.Sprintf(`{
  "session_id": %q,
  "agent_mode": "multi-agent",
  "conversation_history": [
    {"Role":"human","Parts":[{"Text":"hello from product chat"}]},
    {"Role":"ai","Parts":[{"Text":"hello"}]}
  ],
  "runtime": {
    "kind": "coding_agent",
    "provider": "muse-cli",
    "workspace_path": %q
  },
  "updated_at": %q
}`, sessionID, runtimeWorkspace, updatedAt)
		filename := "session-" + sessionID + "-conversation.json"
		if err := os.WriteFile(filepath.Join(conversationDir, filename), []byte(data), 0o600); err != nil {
			t.Fatalf("write product conversation: %v", err)
		}
	}

	writeProductChat("product-matching", "_users/default/"+workspacePath, "2026-09-14T10:00:00Z")
	writeProductChat("product-other", "_users/default/Chats/Work/projects/other", "2026-09-14T11:00:00Z")

	sessions, err := ListChatHistorySessionsByKind("default", "chat", 10, 0, workspacePath)
	if err != nil {
		t.Fatalf("list product project chats: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "product-matching" {
		t.Fatalf("sessions = %#v, want only exact workspace-bound product chat", sessions)
	}
}

func TestListWorkProjectChatHistoryResolvesOwnedProjectStorage(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	const (
		userID        = "alice"
		workspacePath = "Chats/Work/projects/demo-12345678"
		sessionID     = "product-chat-1"
	)
	conversationDir := filepath.Join(root, "_users", userID, filepath.FromSlash(workspacePath), "builder", "conversation", "2026-09-15")
	if err := os.MkdirAll(conversationDir, 0o755); err != nil {
		t.Fatalf("mkdir Work conversation dir: %v", err)
	}
	conversation := `{
  "session_id": "product-chat-1",
  "agent_mode": "multi-agent",
  "conversation_history": [
    {"Role":"human","Parts":[{"Text":"saved Work question"}]},
    {"Role":"ai","Parts":[{"Text":"saved Work answer"}]}
  ],
  "runtime": {
    "kind": "coding_agent",
    "provider": "claude-code",
    "workspace_path": "_users/alice/Chats/Work/projects/demo-12345678"
  },
  "user_id": "alice",
  "updated_at": "2026-09-15T05:00:00Z"
}`
	conversationPath := filepath.Join(conversationDir, chatHistoryConversationFileName(sessionID))
	if err := os.WriteFile(conversationPath, []byte(conversation), 0o600); err != nil {
		t.Fatalf("write Work conversation: %v", err)
	}

	sessions, err := ListChatHistorySessionsByKind(userID, "chat", 25, 0, workspacePath)
	if err != nil {
		t.Fatalf("list Work project chats: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != sessionID || sessions[0].Query != "saved Work question" {
		t.Fatalf("Work project history = %#v, want owned project transcript", sessions)
	}
	if !strings.HasPrefix(sessions[0].ConversationPath, "_users/alice/Chats/Work/projects/") {
		t.Fatalf("conversation path = %q, want owned Work path", sessions[0].ConversationPath)
	}
}

func TestAppendLiveInputUsesOwnedWorkProjectConversation(t *testing.T) {
	const (
		userID        = "alice"
		workspacePath = "_users/alice/Chats/Work/projects/demo-12345678"
		sessionID     = "product-chat-live-input"
	)
	conversationPath := workspacePath + "/builder/conversation/2026-09-16/" + chatHistoryConversationFileName(sessionID)
	conversation := `{
  "session_id": "product-chat-live-input",
  "agent_mode": "multi-agent",
  "conversation_history": [{"Role":"human","Parts":[{"Text":"original question"}]}],
  "runtime": {"provider":"claude-code","workspace_path":"_users/alice/Chats/Work/projects/demo-12345678"},
  "user_id": "alice",
  "updated_at": "2026-09-16T05:00:00Z"
}`
	workspace := &mockWorkspaceAPI{files: map[string]string{conversationPath: conversation}}
	workspaceServer := httptest.NewServer(workspace)
	defer workspaceServer.Close()
	t.Setenv("WORKSPACE_API_URL", workspaceServer.URL)

	api := &StreamingAPI{}
	api.appendLiveInputToPersistedChatHistory(userID, sessionID, workspacePath, "resumed follow-up")

	var record struct {
		ConversationHistory []llmtypes.MessageContent `json:"conversation_history"`
	}
	if err := json.Unmarshal([]byte(workspace.files[conversationPath]), &record); err != nil {
		t.Fatalf("decode Work conversation: %v", err)
	}
	if len(record.ConversationHistory) != 2 || len(record.ConversationHistory[1].Parts) != 1 || chatHistoryPartText(record.ConversationHistory[1].Parts[0]) != "resumed follow-up" {
		t.Fatalf("persisted Work history = %#v, want resumed follow-up appended", record.ConversationHistory)
	}
}

func TestWorkProjectChatHistoryConversationPathUsesProjectBuilderFolder(t *testing.T) {
	timestamp := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	got, ok := workProjectChatHistoryConversationPath(
		"default",
		"_users/default/Chats/Work/projects/demo-12345678",
		"product-chat-1",
		timestamp,
	)
	if !ok {
		t.Fatal("expected Work project workspace to use project-owned chat history")
	}
	want := "_users/default/Chats/Work/projects/demo-12345678/builder/conversation/2026-09-14/session-product-chat-1-conversation.json"
	if got != want {
		t.Fatalf("conversation path = %q, want %q", got, want)
	}

	got, ok = workProjectChatHistoryConversationPath(
		"alice",
		"Chats/Work/projects/demo-12345678",
		"product-chat-2",
		timestamp,
	)
	if !ok || !strings.HasPrefix(got, "_users/alice/Chats/Work/projects/demo-12345678/builder/conversation/") {
		t.Fatalf("unscoped Work path = %q, ok=%v", got, ok)
	}

	if got, ok := workProjectChatHistoryConversationPath("default", "Chats/Video/projects/demo", "chat-3", timestamp); ok || got != "" {
		t.Fatalf("other product path = %q, ok=%v; want unchanged global storage", got, ok)
	}
}

func TestDeleteWorkProjectChatRemovesProjectAndLegacyCopies(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	const (
		userID        = "alice"
		sessionID     = "product-chat-1"
		workspacePath = "Chats/Work/projects/demo"
	)
	projectConversation := filepath.Join(root, "_users", userID, filepath.FromSlash(workspacePath), "builder", "conversation", "2026-09-14", chatHistoryConversationFileName(sessionID))
	legacyConversation := filepath.Join(root, "_users", userID, "chat_history", "2026-09-14", chatHistoryConversationFileName(sessionID))
	for _, conversation := range []string{projectConversation, legacyConversation} {
		if err := os.MkdirAll(filepath.Dir(conversation), 0o755); err != nil {
			t.Fatalf("mkdir conversation parent: %v", err)
		}
		if err := os.WriteFile(conversation, []byte(`{"session_id":"product-chat-1"}`), 0o600); err != nil {
			t.Fatalf("write conversation: %v", err)
		}
	}

	result, err := DeleteChatHistorySession(userID, sessionID, workspacePath)
	if err != nil {
		t.Fatalf("delete Work chat: %v", err)
	}
	if result.DeletedCount != 2 {
		t.Fatalf("deleted count = %d, want project and legacy copies", result.DeletedCount)
	}
	for _, conversation := range []string{projectConversation, legacyConversation} {
		if _, err := os.Stat(conversation); !os.IsNotExist(err) {
			t.Fatalf("conversation still exists or stat failed: %s: %v", conversation, err)
		}
	}
}

func TestNormalizeRestoredChatHistoryConversationPathAllowsOnlyOwnedWorkProject(t *testing.T) {
	owned := "_users/alice/Chats/Work/projects/demo/builder/conversation/2026-09-14/session-chat-1-conversation.json"
	if got, ok := normalizeRestoredChatHistoryConversationPath("alice", owned); !ok || got != owned {
		t.Fatalf("owned Work conversation path = %q, ok=%v", got, ok)
	}
	otherUser := "_users/bob/Chats/Work/projects/demo/builder/conversation/2026-09-14/session-chat-1-conversation.json"
	if got, ok := normalizeRestoredChatHistoryConversationPath("alice", otherUser); ok || got != "" {
		t.Fatalf("other user's Work conversation path = %q, ok=%v", got, ok)
	}
}

func TestCopyChatHistoryConversationIfNeededPreservesLegacyTranscript(t *testing.T) {
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{}})
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	sourcePath := "_users/default/chat_history/2026-09-14/session-legacy-conversation.json"
	destinationPath := "_users/default/Chats/Work/projects/demo/builder/conversation/2026-09-14/session-legacy-conversation.json"
	if err := writeRawFileToWorkspace(context.Background(), sourcePath, `{"session_id":"legacy","ui_events":[{"type":"tool"}]}`); err != nil {
		t.Fatalf("write legacy transcript: %v", err)
	}
	if err := copyChatHistoryConversationIfNeeded(sourcePath, destinationPath); err != nil {
		t.Fatalf("copy legacy transcript: %v", err)
	}

	got, exists, err := readFileFromWorkspace(context.Background(), destinationPath)
	if err != nil || !exists {
		t.Fatalf("read project transcript: exists=%v err=%v", exists, err)
	}
	if !strings.Contains(got, `"ui_events"`) {
		t.Fatalf("project transcript lost durable UI events: %s", got)
	}
	if _, exists, err := readFileFromWorkspace(context.Background(), sourcePath); err != nil || !exists {
		t.Fatalf("legacy fallback should remain readable: exists=%v err=%v", exists, err)
	}

	if err := writeRawFileToWorkspace(context.Background(), destinationPath, `{"session_id":"newer"}`); err != nil {
		t.Fatalf("overwrite project transcript for test: %v", err)
	}
	if err := copyChatHistoryConversationIfNeeded(sourcePath, destinationPath); err != nil {
		t.Fatalf("second copy: %v", err)
	}
	got, _, _ = readFileFromWorkspace(context.Background(), destinationPath)
	if !strings.Contains(got, `"newer"`) {
		t.Fatalf("existing project transcript was overwritten: %s", got)
	}
}

// A transcript on disk that the index does not know about must still be listed.
// It was not: the workflow lister returned a complete index verbatim, so a chat
// whose write did not also update the index stayed out of /resume permanently —
// the user was offered an older conversation and resumed that instead.
func TestListWorkflowChatHistoryFindsConversationMissingFromCompleteIndex(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	workflowPath := "Workflow/unindexed-workflow"
	convDir := filepath.Join(root, filepath.FromSlash(workflowPath), "builder", "conversation", "2026-08-03")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("mkdir conversation dir: %v", err)
	}
	conversation := `{"session_id":"unindexed-chat","conversation_history":[{"role":"human","parts":[{"type":"text","text":"the chat that vanished"}]}],"updated_at":"2026-08-03T19:09:19+05:30"}`
	if err := os.WriteFile(filepath.Join(convDir, "session-unindexed-chat-conversation.json"), []byte(conversation), 0o600); err != nil {
		t.Fatalf("write conversation: %v", err)
	}

	// An index that claims to be complete and has never heard of the file above.
	index := newChatHistoryIndex()
	index.Complete = true
	indexPath := filepath.Join(root, filepath.FromSlash(workflowPath), "builder", "conversation", chatHistoryIndexFileName)
	if err := writeLocalChatHistoryIndex(indexPath, &index); err != nil {
		t.Fatalf("write workflow chat index: %v", err)
	}

	sessions, err := ListChatHistorySessions("default", 10, 0, workflowPath)
	if err != nil {
		t.Fatalf("list workflow sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "unindexed-chat" {
		t.Fatalf("conversation missing from listing: %#v", sessions)
	}

	// And the listing repairs the index, so the next read is served from cache.
	repaired, exists := readLocalChatHistoryIndex(indexPath)
	if !exists || len(repaired.Entries) != 1 {
		t.Fatalf("index not backfilled: exists=%v entries=%d", exists, len(repaired.Entries))
	}
}

func TestListChatHistorySessionsByKindFiltersBeforePagination(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	workspacePath := "Workflow/kind-filter"
	convDir := filepath.Join(root, filepath.FromSlash(workspacePath), "builder", "conversation", "2026-08-22")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("mkdir conversation dir: %v", err)
	}
	writeConversation := func(sessionID string, modifiedAt time.Time) {
		t.Helper()
		conversation := fmt.Sprintf(`{"session_id":%q,"conversation_history":[{"role":"human","parts":[{"type":"text","text":"%s"}]}]}`, sessionID, sessionID)
		filePath := filepath.Join(convDir, "session-"+sessionID+"-conversation.json")
		if err := os.WriteFile(filePath, []byte(conversation), 0o600); err != nil {
			t.Fatalf("write %s: %v", sessionID, err)
		}
		if err := os.Chtimes(filePath, modifiedAt, modifiedAt); err != nil {
			t.Fatalf("set mtime for %s: %v", sessionID, err)
		}
	}

	base := time.Date(2026, time.August, 22, 10, 0, 0, 0, time.UTC)
	// These newer schedule transcripts fill a mixed first page. The ordinary
	// chat is deliberately older, reproducing the empty Recent tab regression.
	for i := 0; i < 30; i++ {
		writeConversation(fmt.Sprintf("schedule-cron--job_%02d", i), base.Add(time.Duration(i)*time.Minute))
	}
	writeConversation("ordinary-builder-chat", base.Add(-time.Hour))

	sessions, err := ListChatHistorySessionsByKind("default", "chat", 25, 0, workspacePath)
	if err != nil {
		t.Fatalf("list chat sessions by kind: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "ordinary-builder-chat" {
		t.Fatalf("chat-kind page = %#v, want ordinary builder chat", sessions)
	}
}

func TestPersistChatConversationUpdatesMetadataIndex(t *testing.T) {
	workspace := &mockWorkspaceAPI{files: map[string]string{}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	history := []llmtypes.MessageContent{{
		Role: llmtypes.ChatMessageTypeHuman,
		Parts: []llmtypes.ContentPart{
			llmtypes.TextContent{Text: "the indexed conversation title"},
		},
	}}
	conversationPath := "Workflow/demo/builder/conversation/2026-08-03/session-save-index-conversation.json"
	api := &StreamingAPI{}
	api.persistChatConversationToPathWithTerminalSession("save-index", "", "workflow", "default", history, &ChatHistoryAgentRuntime{
		Kind:         "coding_agent",
		Provider:     "codex",
		WorkshopMode: "workshop",
	}, nil, conversationPath)

	indexPath := "Workflow/demo/builder/conversation/" + chatHistoryIndexFileName
	workspace.mu.Lock()
	conversationData, conversationExists := workspace.files[conversationPath]
	indexData, indexExists := workspace.files[indexPath]
	workspace.mu.Unlock()
	if !conversationExists || !indexExists {
		t.Fatalf("persisted conversation=%v index=%v, want both", conversationExists, indexExists)
	}

	var index chatHistoryIndex
	if err := json.Unmarshal([]byte(indexData), &index); err != nil {
		t.Fatalf("decode persisted index: %v", err)
	}
	entry, ok := index.Entries[conversationPath]
	if !ok {
		t.Fatalf("missing index entry for %s: %#v", conversationPath, index.Entries)
	}
	if entry.Session.Query != "the indexed conversation title" || entry.Session.Status != "completed" || entry.Session.WorkspacePath != "Workflow/demo" {
		t.Fatalf("unexpected persisted metadata: %#v", entry.Session)
	}
	if entry.SourceSize != int64(len(conversationData)) {
		t.Fatalf("source size = %d, want %d", entry.SourceSize, len(conversationData))
	}
	if !entry.AttributionVerified {
		t.Fatal("newly persisted conversation must have verified attribution")
	}
}

func TestUpdatePersistedChatHistoryIndexCarriesTitleAcrossDatedPaths(t *testing.T) {
	workspace := &mockWorkspaceAPI{files: map[string]string{}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	oldPath := "Workflow/demo/builder/conversation/2026-09-15/session-resumed-chat-conversation.json"
	newPath := "Workflow/demo/builder/conversation/2026-09-16/session-resumed-chat-conversation.json"
	indexPath := "Workflow/demo/builder/conversation/" + chatHistoryIndexFileName
	index := newChatHistoryIndex()
	index.Entries[oldPath] = chatHistoryIndexEntry{Session: ChatHistorySession{
		SessionID: "resumed-chat",
		Title:     "Release follow-up",
		UpdatedAt: "2026-09-15T10:00:00Z",
	}}
	encoded, _ := json.Marshal(index)
	workspace.files[indexPath] = string(encoded)

	history := []llmtypes.MessageContent{{
		Role:  llmtypes.ChatMessageTypeHuman,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "check again"}},
	}}
	if err := updatePersistedChatHistoryIndex("default", "resumed-chat", "workflow", history, nil, newPath, 123, time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC), nil); err != nil {
		t.Fatalf("update index: %v", err)
	}

	workspace.mu.Lock()
	updated := workspace.files[indexPath]
	workspace.mu.Unlock()
	var got chatHistoryIndex
	if err := json.Unmarshal([]byte(updated), &got); err != nil {
		t.Fatalf("decode updated index: %v", err)
	}
	if got.Entries[newPath].Session.Title != "Release follow-up" {
		t.Fatalf("new dated entry title = %q, want inherited custom title", got.Entries[newPath].Session.Title)
	}
}

func TestListWorkflowHistoryRepairsMissingTitleFromOlderDatedEntry(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	workflowPath := "Workflow/demo"
	conversationRoot := filepath.Join(root, filepath.FromSlash(workflowPath), "builder", "conversation")
	oldDir := filepath.Join(conversationRoot, "2026-09-15")
	newDir := filepath.Join(conversationRoot, "2026-09-16")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fileName := "session-resumed-chat-conversation.json"
	oldLocalPath := filepath.Join(oldDir, fileName)
	newLocalPath := filepath.Join(newDir, fileName)
	if err := os.WriteFile(oldLocalPath, []byte(`{"session_id":"resumed-chat"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newLocalPath, []byte(`{"session_id":"resumed-chat"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(24 * time.Hour)
	if err := os.Chtimes(oldLocalPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newLocalPath, newTime, newTime); err != nil {
		t.Fatal(err)
	}
	oldInfo, _ := os.Stat(oldLocalPath)
	newInfo, _ := os.Stat(newLocalPath)
	oldWorkspacePath := workflowPath + "/builder/conversation/2026-09-15/" + fileName
	newWorkspacePath := workflowPath + "/builder/conversation/2026-09-16/" + fileName
	index := newChatHistoryIndex()
	putLocalChatHistoryIndexEntry(&index, localChatHistoryFile{sessionID: "resumed-chat", convPath: oldLocalPath, workspacePath: oldWorkspacePath, modTime: oldInfo.ModTime(), size: oldInfo.Size()}, ChatHistorySession{SessionID: "resumed-chat", Title: "Release follow-up", UpdatedAt: oldTime.Format(time.RFC3339)})
	putLocalChatHistoryIndexEntry(&index, localChatHistoryFile{sessionID: "resumed-chat", convPath: newLocalPath, workspacePath: newWorkspacePath, modTime: newInfo.ModTime(), size: newInfo.Size()}, ChatHistorySession{SessionID: "resumed-chat", Query: "check again", UpdatedAt: newTime.Format(time.RFC3339)})
	if err := writeLocalChatHistoryIndex(filepath.Join(conversationRoot, chatHistoryIndexFileName), &index); err != nil {
		t.Fatal(err)
	}

	sessions, ok := listWorkflowBuilderHistoryFromDisk("default", workflowPath, 10, "chat")
	if !ok || len(sessions) != 1 {
		t.Fatalf("list ok=%v sessions=%#v", ok, sessions)
	}
	if sessions[0].Title != "Release follow-up" {
		t.Fatalf("listed title = %q, want title from older dated entry", sessions[0].Title)
	}
	repaired, exists := readLocalChatHistoryIndex(filepath.Join(conversationRoot, chatHistoryIndexFileName))
	if !exists || repaired.Entries[newWorkspacePath].Session.Title != "Release follow-up" {
		t.Fatalf("newest index entry was not repaired: %#v", repaired.Entries[newWorkspacePath])
	}
}

// Reproduces the customer-login/apollo-mcp incident: a session's first save
// wrote user_id="default" (real attribution not resolved yet), every later
// save correctly rewrote the conversation file with the real user, but the
// index entry stayed frozen at "default"/"System / legacy" because
// updatePersistedChatHistoryIndex logs and continues on a failed write. The
// old "backfill on next listing" only re-derived Username from the index's
// own (already wrong) UserID, so it could never self-correct. This must both
// fix the returned session in-memory and self-heal the persisted index entry.
func TestRepairStaleChatHistoryAttributionFixesFrozenDefaultFromRealConversationFile(t *testing.T) {
	workspace := &mockWorkspaceAPI{files: map[string]string{}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	conversationPath := "Workflow/customer-login/builder/conversation/2026-09-09/session-d5419bb4-conversation.json"
	indexPath := "Workflow/customer-login/builder/conversation/" + chatHistoryIndexFileName

	workspace.mu.Lock()
	workspace.files[conversationPath] = `{"session_id":"d5419bb4","user_id":"e697742fc7eab8bc1cf3b7658885b8c2","username":"saurabh","conversation_history":[]}`
	indexJSON, _ := json.Marshal(chatHistoryIndex{
		Version: chatHistoryIndexVersion,
		Entries: map[string]chatHistoryIndexEntry{
			conversationPath: {
				Session: ChatHistorySession{
					SessionID:        "d5419bb4",
					UserID:           "default",
					Username:         "System / legacy",
					ConversationPath: conversationPath,
				},
			},
		},
	})
	workspace.files[indexPath] = string(indexJSON)
	workspace.mu.Unlock()

	session := ChatHistorySession{
		SessionID:        "d5419bb4",
		UserID:           "default",
		Username:         "System / legacy",
		ConversationPath: conversationPath,
	}
	repairStaleChatHistoryAttribution(&session)

	if session.UserID != "e697742fc7eab8bc1cf3b7658885b8c2" || session.Username != "saurabh" {
		t.Fatalf("in-memory session not repaired: %#v", session)
	}

	workspace.mu.Lock()
	indexData := workspace.files[indexPath]
	workspace.mu.Unlock()
	var index chatHistoryIndex
	if err := json.Unmarshal([]byte(indexData), &index); err != nil {
		t.Fatalf("decode repaired index: %v", err)
	}
	entry, ok := index.Entries[conversationPath]
	if !ok {
		t.Fatalf("index entry disappeared after repair: %#v", index.Entries)
	}
	if entry.Session.UserID != "e697742fc7eab8bc1cf3b7658885b8c2" || entry.Session.Username != "saurabh" {
		t.Fatalf("persisted index entry not repaired: %#v", entry.Session)
	}
	if !entry.AttributionVerified {
		t.Fatal("repaired index entry must remember that attribution was checked")
	}
}

// A conversation file that also genuinely has no real user (true legacy data,
// not a bug) must be left alone -- "System / legacy" is the correct label there.
func TestRepairStaleChatHistoryAttributionLeavesGenuineLegacySessionsAlone(t *testing.T) {
	workspace := &mockWorkspaceAPI{files: map[string]string{}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	conversationPath := "Workflow/old-workflow/builder/conversation/2026-01-01/session-legacy-conversation.json"
	indexPath := "Workflow/old-workflow/builder/conversation/" + chatHistoryIndexFileName
	workspace.mu.Lock()
	workspace.files[conversationPath] = `{"session_id":"legacy","conversation_history":[]}`
	indexJSON, _ := json.Marshal(chatHistoryIndex{
		Version: chatHistoryIndexVersion,
		Entries: map[string]chatHistoryIndexEntry{
			conversationPath: {
				Session: ChatHistorySession{
					SessionID:        "legacy",
					UserID:           "default",
					Username:         "System / legacy",
					ConversationPath: conversationPath,
				},
			},
		},
	})
	workspace.files[indexPath] = string(indexJSON)
	workspace.mu.Unlock()

	session := ChatHistorySession{
		SessionID:        "legacy",
		UserID:           "default",
		Username:         "System / legacy",
		ConversationPath: conversationPath,
	}
	repairStaleChatHistoryAttribution(&session)

	if session.UserID != "default" || session.Username != "System / legacy" {
		t.Fatalf("genuinely legacy session should stay unchanged: %#v", session)
	}
	if !session.attributionVerified {
		t.Fatal("genuinely legacy session must be marked verified after the first check")
	}

	workspace.mu.Lock()
	indexData := workspace.files[indexPath]
	workspace.mu.Unlock()
	var index chatHistoryIndex
	if err := json.Unmarshal([]byte(indexData), &index); err != nil {
		t.Fatalf("decode verified legacy index: %v", err)
	}
	entry := index.Entries[conversationPath]
	if !entry.AttributionVerified {
		t.Fatal("genuinely legacy attribution must be persisted as verified")
	}

	// A later page load reconstructs the row from the index. Even if the full
	// transcript is temporarily unavailable, the verified row must not reopen it.
	reloaded := chatHistorySessionsFromIndex(index, "default", "Workflow/old-workflow")
	if len(reloaded) != 1 || !reloaded[0].attributionVerified {
		t.Fatalf("verified state not restored from index: %#v", reloaded)
	}
	workspace.mu.Lock()
	delete(workspace.files, conversationPath)
	workspace.mu.Unlock()
	repairStaleChatHistoryAttribution(&reloaded[0])
	if !reloaded[0].attributionVerified || reloaded[0].UserID != "default" {
		t.Fatalf("verified legacy session changed on repeat check: %#v", reloaded[0])
	}
}

// Renamed from ...CollapsesScheduleRunsBySchedule. That test asserted every run
// of a schedule but the newest was hidden, on the reasoning that schedule-runs.json
// held the detail instead -- it does not (status and duration only, no error text,
// no route into the conversation). The assertion pinned the behavior that hid a
// failed run and a whole day of history, so it is inverted here rather than kept.
func TestListWorkflowChatHistorySessionsKeepsEveryScheduleRun(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	workflowPath := "Workflow/instagram"
	convDir := filepath.Join(root, "Workflow", "instagram", "builder", "conversation", "2026-05-17")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("mkdir workflow conversation dir: %v", err)
	}

	writeConversation := func(sessionID, updatedAt, text string, mtime time.Time) {
		t.Helper()
		data := fmt.Sprintf(`{
  "session_id": %q,
  "agent_mode": "workflow",
  "conversation_history": [{"Role": "human", "Parts": [{"Text": %q}]}],
  "updated_at": %q
}`, sessionID, text, updatedAt)
		path := filepath.Join(convDir, "session-"+sessionID+"-conversation.json")
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatalf("write conversation %s: %v", sessionID, err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("chtimes conversation %s: %v", sessionID, err)
		}
	}

	oldScheduleSession := "schedule-cron--abc12345_100"
	unindexedScheduleSession := "schedule-cron--abc12345_150"
	latestScheduleSession := "schedule-cron--abc12345_200"
	otherScheduleSession := "schedule-cron--def67890_100"
	manualSession := "manual-chat-1"

	writeConversation(oldScheduleSession, "2026-05-17T10:00:00Z", "old schedule run", time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC))
	writeConversation(unindexedScheduleSession, "2026-05-17T11:00:00Z", "unindexed schedule run", time.Date(2026, 5, 17, 11, 0, 0, 0, time.UTC))
	writeConversation(latestScheduleSession, "2026-05-18T10:00:00Z", "latest schedule run", time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC))
	writeConversation(otherScheduleSession, "2026-05-17T12:00:00Z", "other schedule run", time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC))
	writeConversation(manualSession, "2026-05-18T11:00:00Z", "manual chat", time.Date(2026, 5, 18, 11, 0, 0, 0, time.UTC))

	scheduleRuns := fmt.Sprintf(`[
  {"id":"run-new","schedule_id":"sched-a","session_id":%q,"status":"success","started_at":"2026-05-18T10:00:00Z"},
  {"id":"run-old","schedule_id":"sched-a","session_id":%q,"status":"success","started_at":"2026-05-17T10:00:00Z"},
  {"id":"run-other","schedule_id":"sched-b","session_id":%q,"status":"success","started_at":"2026-05-17T12:00:00Z"}
]`, latestScheduleSession, oldScheduleSession, otherScheduleSession)
	if err := os.WriteFile(filepath.Join(root, "Workflow", "instagram", "schedule-runs.json"), []byte(scheduleRuns), 0o600); err != nil {
		t.Fatalf("write schedule-runs: %v", err)
	}

	sessions, err := ListChatHistorySessions("default", 10, 0, workflowPath)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}

	ids := map[string]bool{}
	for _, session := range sessions {
		ids[session.SessionID] = true
	}
	// Each run is its own session, its own file and its own outcome, so each
	// gets a row -- including runs of the same schedule and ones absent from
	// schedule-runs.json.
	if len(sessions) != 5 {
		t.Fatalf("session count = %d, want 5: %#v", len(sessions), sessions)
	}
	for _, want := range []string{
		manualSession, latestScheduleSession, otherScheduleSession,
		oldScheduleSession, unindexedScheduleSession,
	} {
		if !ids[want] {
			t.Fatalf("missing session %q in %#v", want, sessions)
		}
	}
}

func TestDeleteChatHistorySessionDeletesUserDateBucketAndLegacyFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	legacyDir := filepath.Join(root, "_users", "default", "chat_history", "chat-1")
	dateDir := filepath.Join(root, "_users", "default", "chat_history", "2026-05-17")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("mkdir legacy dir: %v", err)
	}
	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		t.Fatalf("mkdir date dir: %v", err)
	}
	legacyPath := filepath.Join(legacyDir, "conversation.json")
	datePath := filepath.Join(dateDir, "session-chat-1-conversation.json")
	if err := os.WriteFile(legacyPath, []byte(`{"session_id":"chat-1","conversation_history":[]}`), 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	if err := os.WriteFile(datePath, []byte(`{"session_id":"chat-1","conversation_history":[]}`), 0o600); err != nil {
		t.Fatalf("write date bucket: %v", err)
	}
	if _, ok, err := listChatHistorySessionsFromDisk("default", "_users/default/chat_history", "", 10, 0); err != nil || !ok {
		t.Fatalf("build deletion test index ok=%v err=%v", ok, err)
	}

	result, err := DeleteChatHistorySession("default", "chat-1", "")
	if err != nil {
		t.Fatalf("delete error = %v", err)
	}
	if result.DeletedCount != 2 {
		t.Fatalf("deleted count = %d, want 2: %#v", result.DeletedCount, result.DeletedPaths)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy conversation still exists or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(datePath); !os.IsNotExist(err) {
		t.Fatalf("date conversation still exists or stat failed unexpectedly: %v", err)
	}
	index, exists := readLocalChatHistoryIndex(filepath.Join(root, "_users", "default", "chat_history", chatHistoryIndexFileName))
	if !exists || len(index.Entries) != 0 {
		t.Fatalf("deleted chat remains in index: exists=%v entries=%#v", exists, index.Entries)
	}
}

func TestDeleteChatHistoryOlderThanUsesJSONTimestampAndSkipsEmpty(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	convDir := filepath.Join(root, "_users", "default", "chat_history", "2026-05-17")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("mkdir conversation dir: %v", err)
	}

	oldWithFreshMTime := filepath.Join(convDir, "session-old-json-conversation.json")
	if err := os.WriteFile(oldWithFreshMTime, []byte(`{
  "session_id": "old-json",
  "conversation_history": [{"Role": "human", "Parts": [{"Text": "old by json"}]}],
  "updated_at": "`+time.Now().AddDate(0, 0, -15).Format(time.RFC3339)+`"
}`), 0o600); err != nil {
		t.Fatalf("write old-json: %v", err)
	}

	newWithOldMTime := filepath.Join(convDir, "session-new-json-conversation.json")
	if err := os.WriteFile(newWithOldMTime, []byte(`{
  "session_id": "new-json",
  "conversation_history": [{"Role": "human", "Parts": [{"Text": "new by json"}]}],
  "updated_at": "`+time.Now().Format(time.RFC3339)+`"
}`), 0o600); err != nil {
		t.Fatalf("write new-json: %v", err)
	}
	oldMTime := time.Now().AddDate(0, 0, -30)
	if err := os.Chtimes(newWithOldMTime, oldMTime, oldMTime); err != nil {
		t.Fatalf("chtimes new-json: %v", err)
	}

	emptyOld := filepath.Join(convDir, "session-empty-old-conversation.json")
	if err := os.WriteFile(emptyOld, []byte(`{
  "session_id": "empty-old",
  "conversation_history": [],
  "updated_at": "`+time.Now().AddDate(0, 0, -15).Format(time.RFC3339)+`"
}`), 0o600); err != nil {
		t.Fatalf("write empty-old: %v", err)
	}

	result, err := DeleteChatHistoryOlderThan("default", 14, "")
	if err != nil {
		t.Fatalf("cleanup error = %v", err)
	}
	if result.DeletedCount != 1 {
		t.Fatalf("deleted count = %d, want 1: %#v", result.DeletedCount, result.DeletedPaths)
	}
	if _, err := os.Stat(oldWithFreshMTime); !os.IsNotExist(err) {
		t.Fatalf("old json conversation still exists or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(newWithOldMTime); err != nil {
		t.Fatalf("new json conversation should remain: %v", err)
	}
	if _, err := os.Stat(emptyOld); err != nil {
		t.Fatalf("empty old conversation should remain: %v", err)
	}
}

func TestDeleteChatHistoryOlderThanWorkProjectRemovesBothCopies(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	const (
		userID        = "alice"
		workspacePath = "Chats/Work/projects/demo"
	)
	oldAt := time.Now().AddDate(0, 0, -15).Format(time.RFC3339)
	newAt := time.Now().Format(time.RFC3339)
	conversation := func(sessionID, updatedAt string, history string) string {
		return `{"session_id": "` + sessionID + `", "workspace_path": "` + workspacePath + `", "conversation_history": [` + history + `], "updated_at": "` + updatedAt + `"}`
	}
	message := `{"Role": "human", "Parts": [{"Text": "hello"}]}`
	writeConversation := func(path, payload string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir conversation parent: %v", err)
		}
		if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
			t.Fatalf("write conversation: %v", err)
		}
	}
	projectDir := filepath.Join(root, "_users", userID, filepath.FromSlash(workspacePath), "builder", "conversation", "2026-09-14")
	legacyDir := filepath.Join(root, "_users", userID, "chat_history", "2026-09-14")
	oldProject := filepath.Join(projectDir, "session-old-chat-conversation.json")
	oldLegacy := filepath.Join(legacyDir, "session-old-chat-conversation.json")
	writeConversation(oldProject, conversation("old-chat", oldAt, message))
	writeConversation(oldLegacy, conversation("old-chat", oldAt, message))
	newProject := filepath.Join(projectDir, "session-new-chat-conversation.json")
	writeConversation(newProject, conversation("new-chat", newAt, message))
	emptyProject := filepath.Join(projectDir, "session-empty-chat-conversation.json")
	writeConversation(emptyProject, conversation("empty-chat", oldAt, ""))

	result, err := DeleteChatHistoryOlderThan(userID, 14, workspacePath)
	if err != nil {
		t.Fatalf("cleanup Work chats: %v", err)
	}
	if result.DeletedCount != 2 {
		t.Fatalf("deleted count = %d, want project and legacy copies of the old chat: %#v", result.DeletedCount, result.DeletedPaths)
	}
	for _, gone := range []string{oldProject, oldLegacy} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("conversation still exists or stat failed: %s: %v", gone, err)
		}
	}
	for _, kept := range []string{newProject, emptyProject} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("conversation should remain: %s: %v", kept, err)
		}
	}
}

func TestDeleteChatHistoryOlderThanWorkflowUsesJSONTimestampAndSkipsEmpty(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	convDir := filepath.Join(root, "Workflow", "instagram", "builder", "conversation", "2026-05-17")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("mkdir workflow conversation dir: %v", err)
	}

	oldWorkflow := filepath.Join(convDir, "session-workflow-old-conversation.json")
	if err := os.WriteFile(oldWorkflow, []byte(`{
  "session_id": "workflow-old",
  "conversation_history": [{"Role": "human", "Parts": [{"Text": "old workflow"}]}],
  "updated_at": "`+time.Now().AddDate(0, 0, -15).Format(time.RFC3339)+`"
}`), 0o600); err != nil {
		t.Fatalf("write workflow-old: %v", err)
	}

	emptyWorkflow := filepath.Join(convDir, "session-workflow-empty-conversation.json")
	if err := os.WriteFile(emptyWorkflow, []byte(`{
  "session_id": "workflow-empty",
  "conversation_history": [],
  "updated_at": "`+time.Now().AddDate(0, 0, -15).Format(time.RFC3339)+`"
}`), 0o600); err != nil {
		t.Fatalf("write workflow-empty: %v", err)
	}

	result, err := DeleteChatHistoryOlderThan("default", 14, "Workflow/instagram")
	if err != nil {
		t.Fatalf("cleanup error = %v", err)
	}
	if result.DeletedCount != 1 {
		t.Fatalf("deleted count = %d, want 1: %#v", result.DeletedCount, result.DeletedPaths)
	}
	if _, err := os.Stat(oldWorkflow); !os.IsNotExist(err) {
		t.Fatalf("old workflow conversation still exists or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(emptyWorkflow); err != nil {
		t.Fatalf("empty workflow conversation should remain: %v", err)
	}
}

func TestReadUserChatHistoryConversationDirectPrefersNewestDateBucket(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	oldDir := filepath.Join(root, "_users", "default", "chat_history", "chat-1")
	newDir := filepath.Join(root, "_users", "default", "chat_history", "2026-05-17")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatalf("mkdir legacy dir: %v", err)
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatalf("mkdir date dir: %v", err)
	}
	legacy := []byte(`{"session_id":"chat-1","updated_at":"2026-05-16T10:00:00Z","conversation_history":[]}`)
	dateBucket := []byte(`{"session_id":"chat-1","updated_at":"2026-05-17T10:00:00Z","conversation_history":[]}`)
	if err := os.WriteFile(filepath.Join(oldDir, "conversation.json"), legacy, 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "session-chat-1-conversation.json"), dateBucket, 0o600); err != nil {
		t.Fatalf("write date bucket: %v", err)
	}

	got, ok, err := readUserChatHistoryConversationDirect("default", "chat-1")
	if err != nil {
		t.Fatalf("direct read error = %v", err)
	}
	if !ok {
		t.Fatal("direct read ok = false, want true")
	}
	if string(got) != string(dateBucket) {
		t.Fatalf("direct read = %s, want %s", got, dateBucket)
	}
}

func TestParseLocalChatHistorySessionIncludesRuntime(t *testing.T) {
	data := `{
  "session_id": "session-1",
  "agent_mode": "simple",
  "runtime": {
    "kind": "coding_agent",
    "provider": "claude-code",
    "model_id": "claude-code",
    "external_session_id": "claude-session-1",
    "resume_supported": true,
    "resume_flag": "--resume",
    "workspace_path": "Workflow/example"
  },
  "conversation_history": [
    {
      "Role": "human",
      "Parts": [{"Text": "hello"}]
    }
  ],
  "updated_at": "2026-05-15T10:00:00Z"
}`

	session, ok := parseLocalChatHistorySession("default", "_users/default/chat_history", "", "fallback", data, time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC))
	if !ok {
		t.Fatal("expected session to parse")
	}
	if session.Runtime == nil {
		t.Fatal("expected runtime metadata")
	}
	if session.Runtime.Provider != "claude-code" || session.Runtime.ExternalSessionID != "claude-session-1" || !session.Runtime.ResumeSupported {
		t.Fatalf("unexpected runtime metadata: %#v", session.Runtime)
	}
}

func TestParseLocalChatHistorySessionPreservesCustomTitle(t *testing.T) {
	data := `{
		"session_id":"named-chat",
		"title":"GPT Live handoff",
		"agent_mode":"multi_agent",
		"conversation_history":[{"Role":"human","Parts":[{"Text":"check this file"}]}]
	}`
	session, ok := parseLocalChatHistorySession("default", "_users/default/chat_history", "", "fallback", data, time.Now())
	if !ok {
		t.Fatal("expected session")
	}
	if session.Title != "GPT Live handoff" {
		t.Fatalf("title = %q", session.Title)
	}
}

func TestNormalizeChatHistoryTitle(t *testing.T) {
	if got, err := normalizeChatHistoryTitle("  GPT   Live\n handoff  "); err != nil || got != "GPT Live handoff" {
		t.Fatalf("normalize = %q, %v", got, err)
	}
	if _, err := normalizeChatHistoryTitle("   "); err == nil {
		t.Fatal("expected empty title to fail")
	}
}

func TestRenameWorkChatBeforeFirstPersistenceRetainsTitle(t *testing.T) {
	workspace := &mockWorkspaceAPI{files: map[string]string{}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	const (
		userID        = "rename-before-save-user"
		sessionID     = "product-rename-before-save"
		workspacePath = "Chats/Work/projects/new-project"
		title         = "Latency investigation"
	)

	if err := RenameChatHistorySession(userID, sessionID, workspacePath, title); err != nil {
		t.Fatalf("rename before first save: %v", err)
	}
	conversationPath, ok := workProjectChatHistoryConversationPath(userID, workspacePath, sessionID, time.Now())
	if !ok {
		t.Fatal("expected Work project conversation path")
	}
	if _, exists := workspace.files[conversationPath]; exists {
		t.Fatal("rename must not fabricate a transcript")
	}

	history := []llmtypes.MessageContent{{
		Role:  llmtypes.ChatMessageTypeHuman,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "check this Notion board"}},
	}}
	(&StreamingAPI{}).persistChatConversationToPathWithTerminalSession(
		sessionID, "", "multi-agent", userID, history, nil, nil, conversationPath,
	)

	raw, exists := workspace.files[conversationPath]
	if !exists {
		t.Fatal("first transcript was not persisted")
	}
	var record struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatalf("decode first transcript: %v", err)
	}
	if record.Title != title {
		t.Fatalf("first transcript title = %q, want %q", record.Title, title)
	}

	indexPath := chatHistoryIndexWorkspacePath(userID, conversationPath)
	var index chatHistoryIndex
	if err := json.Unmarshal([]byte(workspace.files[indexPath]), &index); err != nil {
		t.Fatalf("decode Work chat index: %v", err)
	}
	entry, exists := index.Entries[conversationPath]
	if !exists || entry.Session.SessionID != sessionID || entry.Session.Title != title || entry.Session.Status != "completed" {
		t.Fatalf("saved index entry = %#v", entry)
	}

	const updatedTitle = "Notion latency investigation"
	if err := RenameChatHistorySession(userID, sessionID, workspacePath, updatedTitle); err != nil {
		t.Fatalf("rename persisted Work chat: %v", err)
	}
	if err := json.Unmarshal([]byte(workspace.files[conversationPath]), &record); err != nil {
		t.Fatalf("decode renamed transcript: %v", err)
	}
	if record.Title != updatedTitle {
		t.Fatalf("renamed transcript title = %q, want %q", record.Title, updatedTitle)
	}
	if err := json.Unmarshal([]byte(workspace.files[indexPath]), &index); err != nil {
		t.Fatalf("decode renamed Work chat index: %v", err)
	}
	if got := index.Entries[conversationPath].Session.Title; got != updatedTitle {
		t.Fatalf("renamed index title = %q, want %q", got, updatedTitle)
	}
}

func TestParseLocalChatHistorySessionSkipsLowSignalTitle(t *testing.T) {
	data := `{
  "session_id": "session-1",
  "agent_mode": "simple",
  "conversation_history": [
    {
      "Role": "human",
      "Parts": [{"Text": "hello"}]
    },
    {
      "Role": "ai",
      "Parts": [{"Text": "Hi"}]
    },
    {
      "Role": "human",
      "Parts": [{"Text": "ok.. which image generated tools do you have"}]
    }
  ],
  "updated_at": "2026-05-15T10:00:00Z"
}`

	session, ok := parseLocalChatHistorySession("default", "_users/default/chat_history", "", "fallback", data, time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC))
	if !ok {
		t.Fatal("expected session to parse")
	}
	if session.Query != "ok.. which image generated tools do you have" {
		t.Fatalf("expected substantive query title, got %q", session.Query)
	}
}

func TestParseLocalChatHistorySessionUsesLatestSubstantiveTitle(t *testing.T) {
	data := `{
  "session_id": "session-1",
  "agent_mode": "workflow",
  "conversation_history": [
    {
      "Role": "human",
      "Parts": [{"Text": "Explain this codebase"}]
    },
    {
      "Role": "ai",
      "Parts": [{"Text": "Sure."}]
    },
    {
      "Role": "human",
      "Parts": [{"Text": "what did you fix last time?"}]
    }
  ],
  "updated_at": "2026-05-15T10:00:00Z"
}`

	session, ok := parseLocalChatHistorySession("default", "Workflow/example", "Workflow/example", "fallback", data, time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC))
	if !ok {
		t.Fatal("expected session to parse")
	}
	if session.Query != "what did you fix last time?" {
		t.Fatalf("expected latest substantive query title, got %q", session.Query)
	}
}

func TestParseLocalChatHistorySessionIgnoresTrailingLowSignalTitle(t *testing.T) {
	data := `{
  "session_id": "session-1",
  "agent_mode": "workflow",
  "conversation_history": [
    {
      "Role": "human",
      "Parts": [{"Text": "Explain this codebase"}]
    },
    {
      "Role": "human",
      "Parts": [{"Text": "what did you fix last time?"}]
    },
    {
      "Role": "human",
      "Parts": [{"Text": "ok"}]
    }
  ],
  "updated_at": "2026-05-15T10:00:00Z"
}`

	session, ok := parseLocalChatHistorySession("default", "Workflow/example", "Workflow/example", "fallback", data, time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC))
	if !ok {
		t.Fatal("expected session to parse")
	}
	if session.Query != "what did you fix last time?" {
		t.Fatalf("expected latest substantive query title, got %q", session.Query)
	}
}

func testAgentWithHandle(sessionID string, provider llmtypes.CodingProviderSessionHandle) *mcpagent.Agent {
	agent := &mcpagent.Agent{}
	mcpagent.ApplyAgentResumeHandle(agent, &mcpagent.AgentSessionHandle{
		SessionID: sessionID,
		OwnerID:   sessionID,
		Provider:  provider,
	})
	return agent
}

type persistenceTestModel struct{}

func (persistenceTestModel) GenerateContent(context.Context, []llmtypes.MessageContent, ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	return &llmtypes.ContentResponse{}, nil
}
func (persistenceTestModel) GetModelID() string { return "persistence-test" }
func (persistenceTestModel) GetModelMetadata(string) (*llmtypes.ModelMetadata, error) {
	return &llmtypes.ModelMetadata{}, nil
}

func testAgentWithInstructions(t *testing.T, instructions string) *mcpagent.Agent {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	agent, err := mcpagent.NewAgentFromDefinition(context.Background(), mcpagent.AgentDefinition{Instructions: instructions}, mcpagent.RuntimeConfig{
		Model: persistenceTestModel{}, MCPConfigPath: configPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { agent.Close() })
	return agent
}

func requireAgentHandle(t *testing.T, agent *mcpagent.Agent) *mcpagent.AgentSessionHandle {
	t.Helper()
	handle := mcpagent.SnapshotAgentSession(agent)
	if handle == nil {
		t.Fatal("expected agent session handle")
	}
	return handle
}

func TestCaptureChatHistoryAgentRuntimeStoresCLIResumeID(t *testing.T) {
	api := &StreamingAPI{}

	runtime := api.captureChatHistoryAgentRuntime("session-1", "claude-code", "claude-code", "Workflow/example",
		testAgentWithHandle("session-1", llmtypes.CodingProviderSessionHandle{
			Provider: "claude-code", Model: "claude-code", NativeSessionID: "claude-session-1",
		}))

	if runtime == nil {
		t.Fatal("expected runtime metadata")
	}
	if runtime.Kind != "coding_agent" || runtime.Provider != "claude-code" {
		t.Fatalf("unexpected runtime identity: %#v", runtime)
	}
	if runtime.ExternalSessionID != "claude-session-1" || !runtime.ResumeSupported || runtime.ResumeFlag != "--resume" {
		t.Fatalf("unexpected resume metadata: %#v", runtime)
	}
}

func TestCaptureChatHistoryAgentRuntimeStoresCodexThreadID(t *testing.T) {
	api := &StreamingAPI{}

	runtime := api.captureChatHistoryAgentRuntime("codex-session", "codex-cli", "gpt-5.4", "Workflow/example",
		testAgentWithHandle("codex-session", llmtypes.CodingProviderSessionHandle{
			Provider: "codex-cli", Model: "gpt-5.4", NativeSessionID: "codex-thread-1", ProjectDirID: "Workflow/example",
		}))

	if runtime == nil {
		t.Fatal("expected runtime metadata")
	}
	if runtime.Kind != "coding_agent" || runtime.Provider != "codex-cli" {
		t.Fatalf("unexpected runtime identity: %#v", runtime)
	}
	if runtime.ExternalSessionID != "codex-thread-1" || !runtime.ResumeSupported || runtime.ResumeFlag != "resume" {
		t.Fatalf("unexpected Codex resume metadata: %#v", runtime)
	}
	if runtime.ProjectDirID != "Workflow/example" {
		t.Fatalf("runtime project dir ID = %q", runtime.ProjectDirID)
	}
}

func TestCaptureChatHistoryAgentRuntimeStoresPiSessionID(t *testing.T) {
	api := &StreamingAPI{}

	runtime := api.captureChatHistoryAgentRuntime("pi-session", "pi-cli", "google/gemini-3.5-flash", "Workflow/example",
		testAgentWithHandle("pi-session", llmtypes.CodingProviderSessionHandle{
			Provider: "pi-cli", Model: "google/gemini-3.5-flash", NativeSessionID: "mlp-pi-session-1",
		}))

	if runtime == nil {
		t.Fatal("expected runtime metadata")
	}
	if runtime.Kind != "coding_agent" || runtime.Provider != "pi-cli" {
		t.Fatalf("unexpected runtime identity: %#v", runtime)
	}
	if runtime.ExternalSessionID != "mlp-pi-session-1" || !runtime.ResumeSupported || runtime.ResumeFlag != "--session-id" {
		t.Fatalf("unexpected Pi resume metadata: %#v", runtime)
	}
}

func TestCaptureChatHistoryAgentRuntimePersistsAgentSessionHandle(t *testing.T) {
	api := &StreamingAPI{}

	agent := testAgentWithHandle("chat-session-1", llmtypes.CodingProviderSessionHandle{
		Provider:        "codex-cli",
		Transport:       llmtypes.CodingProviderTransportTmux,
		NativeSessionID: "codex-thread-typed",
		TmuxSession:     "tmux-codex-1",
		WorkingDir:      "/workspace",
		ProjectDirID:    "Workflow/example",
		Model:           "gpt-5.4",
		Status:          llmtypes.CodingProviderSessionStatusIdle,
	})

	runtime := api.captureChatHistoryAgentRuntime("chat-session-1", "codex-cli", "gpt-5.4", "Workflow/example", agent)

	if runtime == nil || runtime.AgentSessionHandle == nil {
		t.Fatalf("expected persisted agent session handle, got %#v", runtime)
	}
	if runtime.AgentSessionHandle.Provider.NativeSessionID != "codex-thread-typed" {
		t.Fatalf("runtime handle = %#v", runtime.AgentSessionHandle)
	}
	if runtime.ExternalSessionID != "codex-thread-typed" || runtime.ProjectDirID != "Workflow/example" {
		t.Fatalf("legacy fields not mirrored from handle: %#v", runtime)
	}
	if runtime.Transport != llmtypes.CodingProviderTransportTmux {
		t.Fatalf("runtime transport = %q, want tmux", runtime.Transport)
	}
}

func TestCaptureChatHistoryAgentRuntimeInfersProviderFromSessionHandle(t *testing.T) {
	api := &StreamingAPI{}

	agent := testAgentWithHandle("chat-session-1", llmtypes.CodingProviderSessionHandle{
		Provider:        "codex-cli",
		Transport:       llmtypes.CodingProviderTransportTmux,
		NativeSessionID: "codex-thread-typed",
		ProjectDirID:    "Workflow/example",
		Model:           "gpt-5.4",
	})

	runtime := api.captureChatHistoryAgentRuntime("chat-session-1", "", "", "Workflow/example", agent)

	if runtime == nil {
		t.Fatal("expected runtime metadata")
	}
	if runtime.Kind != "coding_agent" || runtime.Provider != "codex-cli" || runtime.ModelID != "gpt-5.4" {
		t.Fatalf("runtime did not infer coding provider from handle: %#v", runtime)
	}
	if runtime.ExternalSessionID != "codex-thread-typed" || !runtime.ResumeSupported {
		t.Fatalf("runtime did not mirror native session from handle: %#v", runtime)
	}
}

func TestReadChatHistoryRuntimeFromPathReadsDateBucketRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	convDir := filepath.Join(root, "_users", "default", "chat_history", "2026-05-17")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	convPath := filepath.Join(convDir, "session-chat-1-conversation.json")
	if err := os.WriteFile(convPath, []byte(`{
  "session_id": "chat-1",
  "runtime": {
    "kind": "coding_agent",
    "provider": "claude-code",
    "model_id": "claude-sonnet-4-6",
    "external_session_id": "claude-native-1",
    "resume_supported": true,
    "resume_flag": "--resume"
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	runtime, ok, err := ReadChatHistoryRuntimeFromPath("default", "_users/default/chat_history/2026-05-17/session-chat-1-conversation.json")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || runtime == nil {
		t.Fatalf("expected runtime metadata, ok=%v runtime=%#v", ok, runtime)
	}
	if runtime.Provider != "claude-code" || runtime.ExternalSessionID != "claude-native-1" {
		t.Fatalf("unexpected runtime: %#v", runtime)
	}
}

func TestReadChatHistoryRuntimeForSessionReadsWorkflowScopedRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	convDir := filepath.Join(root, "Workflow", "latency-report", "builder", "conversation", "2026-05-20")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	convPath := filepath.Join(convDir, "session-chat-1-conversation.json")
	if err := os.WriteFile(convPath, []byte(`{
  "session_id": "chat-1",
  "workshop_mode": "builder",
  "runtime": {
    "kind": "coding_agent",
    "provider": "claude-code",
    "model_id": "claude-opus-4-6",
    "external_session_id": "claude-native-workflow",
    "resume_supported": true,
    "resume_flag": "--resume"
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	runtime, ok, err := ReadChatHistoryRuntimeForSession("default", "chat-1", "Workflow/latency-report")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || runtime == nil {
		t.Fatalf("expected runtime metadata, ok=%v runtime=%#v", ok, runtime)
	}
	// "builder" on disk is normalized to "workshop" on read (post-merge).
	if runtime.Provider != "claude-code" || runtime.ExternalSessionID != "claude-native-workflow" || runtime.WorkshopMode != "workshop" {
		t.Fatalf("unexpected runtime: %#v", runtime)
	}
}

func TestFindChatHistoryConversationPathForSessionReadsWorkflowScopedPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	convDir := filepath.Join(root, "Workflow", "latency-report", "builder", "conversation", "2026-05-20")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	convPath := filepath.Join(convDir, "session-chat-1-conversation.json")
	if err := os.WriteFile(convPath, []byte(`{
  "session_id": "chat-1",
  "conversation_history": [
    {"Role": "human", "Parts": [{"Text": "continue"}]}
  ],
  "updated_at": "2026-05-20T10:00:00Z"
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok, err := FindChatHistoryConversationPathForSession("default", "chat-1", "Workflow/latency-report")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected conversation path")
	}
	want := "Workflow/latency-report/builder/conversation/2026-05-20/session-chat-1-conversation.json"
	if got != want {
		t.Fatalf("conversation path = %q, want %q", got, want)
	}
}

func TestSeedCodingAgentRuntimeFromRestoredConversationRestoresClaude(t *testing.T) {
	api := &StreamingAPI{}
	agent := testAgentWithHandle("new-ui-session", llmtypes.CodingProviderSessionHandle{})
	runtime := &ChatHistoryAgentRuntime{
		Kind:              "coding_agent",
		Provider:          "claude-code",
		ExternalSessionID: "claude-native-restored",
		ResumeSupported:   true,
	}

	if !api.seedCodingAgentRuntimeFromRestoredConversation("new-ui-session", "claude-code", "", runtime, agent) {
		t.Fatal("expected native resume state to be seeded")
	}

	if got := requireAgentHandle(t, agent).Provider.NativeSessionID; got != "claude-native-restored" {
		t.Fatalf("native session ID = %q", got)
	}
}

func TestSeedCodingAgentRuntimeFromCurrentConversationRestoresClaude(t *testing.T) {
	// This exercises the generic native-resume mechanism with a deliberately
	// empty agent handle. Workflow isolation has its own directory-aware tests.
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "false")
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	convDir := filepath.Join(root, "Workflow", "latency-report", "builder", "conversation", "2026-05-20")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	convPath := filepath.Join(convDir, "session-existing-chat-conversation.json")
	if err := os.WriteFile(convPath, []byte(`{
  "session_id": "existing-chat",
  "workshop_mode": "builder",
  "runtime": {
    "kind": "coding_agent",
    "provider": "claude-code",
    "external_session_id": "claude-native-existing",
    "resume_supported": true,
    "resume_flag": "--resume"
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	api := &StreamingAPI{}
	agent := &mcpagent.Agent{}

	seeded, recoveredRuntime := api.seedCodingAgentRuntimeFromCurrentConversation("existing-chat", "default", "claude-code", "builder", "Workflow/latency-report", agent)
	if !seeded {
		t.Fatal("expected current conversation runtime to seed native resume state")
	}
	if recoveredRuntime == nil {
		t.Fatal("expected the recovered runtime to be returned for the auto-resume re-launch path")
	}
	if got := requireAgentHandle(t, agent).Provider.NativeSessionID; got != "claude-native-existing" {
		t.Fatalf("native session ID = %q", got)
	}
}

func TestSeedCodingAgentRuntimeFromCurrentConversationReplacesIncompleteCursorHandle(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	convDir := filepath.Join(root, "Workflow", "video", "builder", "conversation", "2026-08-08")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(convDir, "session-cursor-chat-conversation.json"), []byte(`{
  "session_id": "cursor-chat",
  "workshop_mode": "builder",
  "runtime": {
    "kind": "coding_agent",
    "provider": "cursor-cli",
    "external_session_id": "cursor-native-restored",
    "resume_supported": true,
    "resume_flag": "--resume",
    "agent_session_handle": {
      "session_id": "cursor-chat",
      "provider": {"provider": "cursor-cli", "transport": "structured", "native_session_id": "cursor-native-restored"}
    }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	api := &StreamingAPI{}
	// A newly configured agent has provider metadata but no native Cursor
	// session. It must not mask the durable handle above.
	agent := testAgentWithHandle("cursor-chat", llmtypes.CodingProviderSessionHandle{
		Provider: "cursor-cli", Transport: "structured", Model: "auto", WorkingDir: "/tmp/video",
	})

	seeded, _ := api.seedCodingAgentRuntimeFromCurrentConversation("cursor-chat", "default", "cursor-cli", "builder", "Workflow/video", agent)
	if !seeded {
		t.Fatal("expected incomplete Cursor handle to be replaced with the persisted native session")
	}
	if got := requireAgentHandle(t, agent).Provider.NativeSessionID; got != "cursor-native-restored" {
		t.Fatalf("native session ID = %q", got)
	}
}

func TestCodingAgentHasNativeResumeRejectsHandleWithoutNativeID(t *testing.T) {
	agent := testAgentWithHandle("chat-1", llmtypes.CodingProviderSessionHandle{
		Provider:   "claude-code",
		Transport:  llmtypes.CodingProviderTransportTmux,
		WorkingDir: "/workspace",
		Model:      "claude-opus-5",
		Status:     llmtypes.CodingProviderSessionStatusIdle,
	})
	if codingAgentHasNativeResume("claude-code", agent) {
		t.Fatal("handle without native session ID must not bypass persisted-runtime recovery")
	}
}

func TestSeedCodingAgentRuntimeFromRestoredConversationRestoresCodex(t *testing.T) {
	// This exercises generic handle restoration, not workflow isolation.
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "false")
	api := &StreamingAPI{}
	agent := &mcpagent.Agent{}
	runtime := &ChatHistoryAgentRuntime{
		Kind:              "coding_agent",
		Provider:          "codex-cli",
		ExternalSessionID: "codex-thread-restored",
		ProjectDirID:      "Workflow/example",
		ResumeSupported:   true,
	}

	if !api.seedCodingAgentRuntimeFromRestoredConversation("codex-ui-session", "codex-cli", "", runtime, agent) {
		t.Fatal("expected Codex resume state to be seeded")
	}

	handle := requireAgentHandle(t, agent)
	if handle.Provider.NativeSessionID != "codex-thread-restored" {
		t.Fatalf("native session ID = %q", handle.Provider.NativeSessionID)
	}
	if handle.Provider.ProjectDirID != "Workflow/example" {
		t.Fatalf("project dir ID = %q", handle.Provider.ProjectDirID)
	}
}

func TestSeedCodingAgentRuntimeFromRestoredConversationRestoresPi(t *testing.T) {
	// This exercises generic handle restoration, not workflow isolation.
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "false")
	api := &StreamingAPI{}
	agent := &mcpagent.Agent{}
	runtime := &ChatHistoryAgentRuntime{
		Kind:              "coding_agent",
		Provider:          "pi-cli",
		ExternalSessionID: "mlp-pi-restored",
		ResumeSupported:   true,
	}

	if !api.seedCodingAgentRuntimeFromRestoredConversation("pi-ui-session", "pi-cli", "", runtime, agent) {
		t.Fatal("expected Pi resume state to be seeded")
	}

	if got := requireAgentHandle(t, agent).Provider.NativeSessionID; got != "mlp-pi-restored" {
		t.Fatalf("native session ID = %q", got)
	}
}

func TestSeedCodingAgentRuntimeFromRestoredConversationDoesNotRestoreLegacyPrompt(t *testing.T) {
	api := &StreamingAPI{}
	currentPrompt := "<current>mode=auto; query agent_browser status</current>"
	agent := testAgentWithInstructions(t, currentPrompt)

	// Old conversation files may still contain persisted prompts and a resolved
	// browser_mode. JSON decoding must ignore those legacy fields, and native
	// resume must preserve the prompt assembled by the current /api/query.
	runtime, err := chatHistoryRuntimeFromJSON([]byte(`{
  "runtime": {
    "kind": "coding_agent",
    "provider": "pi-cli",
    "external_session_id": "mlp-pi-restored",
    "resume_supported": true,
    "system_prompt": "<stale>mode=headless</stale>",
    "appended_system_prompts": ["<stale-browser-state>"],
    "browser_mode": "headless"
  }
}`))
	if err != nil || runtime == nil {
		t.Fatalf("decode legacy runtime: runtime=%#v err=%v", runtime, err)
	}

	if !api.seedCodingAgentRuntimeFromRestoredConversation("pi-ui-session", "pi-cli", "", runtime, agent) {
		t.Fatal("expected Pi resume state to be seeded")
	}
	if got := agent.Definition().Instructions; got != currentPrompt {
		t.Fatalf("native resume overwrote current prompt: got %q want %q", got, currentPrompt)
	}
}

func TestCaptureChatHistoryAgentRuntimeOmitsPromptAndBrowserAvailability(t *testing.T) {
	api := &StreamingAPI{}
	agent := testAgentWithHandle("pi-ui-session", llmtypes.CodingProviderSessionHandle{
		Provider:        "pi-cli",
		NativeSessionID: "mlp-pi-roundtrip",
		ProjectDirID:    "Workflow/example",
		Model:           "pi-cli",
	})
	agentWithPrompt := testAgentWithInstructions(t, "<current>mode=cdp</current>\n\n<dynamic-browser-state>")
	mcpagent.ApplyAgentResumeHandle(agentWithPrompt, mcpagent.SnapshotAgentSession(agent))
	agent = agentWithPrompt
	common.SetSessionBrowserMode("pi-ui-session", "cdp")

	runtime := api.captureChatHistoryAgentRuntime("pi-ui-session", "pi-cli", "pi-cli", "Workflow/example", agent)
	raw, err := json.Marshal(runtime)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"system_prompt", "appended_system_prompts", "browser_mode", "dynamic-browser-state"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("persisted runtime %s contains dynamic field/value %q", raw, forbidden)
		}
	}
}

// TestSeedCodingAgentRuntimeFromRestoredConversationSkipsEmptySystemPrompt
// guards against a runtime saved before the SystemPrompt field existed:
// a missing system prompt must NOT wipe the agent's existing prompt
// (which may have been set by a parallel /api/query path).
func TestSeedCodingAgentRuntimeFromRestoredConversationSkipsEmptySystemPrompt(t *testing.T) {
	api := &StreamingAPI{}
	preExisting := "default agent prompt set elsewhere"
	agent := testAgentWithInstructions(t, preExisting)
	runtime := &ChatHistoryAgentRuntime{
		Kind:              "coding_agent",
		Provider:          "pi-cli",
		ExternalSessionID: "mlp-pi-restored",
		ResumeSupported:   true,
		// SystemPrompt intentionally empty (legacy runtime)
	}

	if !api.seedCodingAgentRuntimeFromRestoredConversation("pi-ui-session", "pi-cli", "", runtime, agent) {
		t.Fatal("expected Pi resume state to be seeded")
	}

	if got := agent.Definition().Instructions; got != preExisting {
		t.Fatalf("agent.Instructions() = %q, want %q unchanged — empty runtime SystemPrompt must NOT clobber a prompt set elsewhere", got, preExisting)
	}
}

func TestSeedCodingAgentRuntimeFromRestoredConversationAppliesAgentSessionHandle(t *testing.T) {
	// This verifies typed handle restoration independently of the workflow
	// directory boundary, which has dedicated same-directory coverage.
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "false")
	api := &StreamingAPI{}
	agent := &mcpagent.Agent{}
	runtime := &ChatHistoryAgentRuntime{
		Kind:     "coding_agent",
		Provider: "codex-cli",
		AgentSessionHandle: &mcpagent.AgentSessionHandle{
			SessionID: "old-chat-session",
			Provider: llmtypes.CodingProviderSessionHandle{
				Provider:        "codex-cli",
				Transport:       llmtypes.CodingProviderTransportTmux,
				NativeSessionID: "codex-thread-from-handle",
				WorkingDir:      "/workspace",
				ProjectDirID:    "Workflow/example",
				Model:           "gpt-5.4",
				Status:          llmtypes.CodingProviderSessionStatusIdle,
			},
		},
	}

	if !api.seedCodingAgentRuntimeFromRestoredConversation("new-ui-session", "codex-cli", "", runtime, agent) {
		t.Fatal("expected typed handle to seed native resume state")
	}
	handle := requireAgentHandle(t, agent)
	if handle.SessionID != "new-ui-session" {
		t.Fatalf("agent SessionID = %q, want current UI session owner", handle.SessionID)
	}
	if handle.Provider.NativeSessionID != "codex-thread-from-handle" {
		t.Fatalf("native session ID = %q", handle.Provider.NativeSessionID)
	}
	if handle.Provider.WorkingDir != "/workspace" {
		t.Fatalf("working dir = %q", handle.Provider.WorkingDir)
	}
}

func TestSeedCodingAgentRuntimeFromRestoredConversationRejectsProviderMismatch(t *testing.T) {
	api := &StreamingAPI{}
	agent := &mcpagent.Agent{}
	runtime := &ChatHistoryAgentRuntime{
		Kind:              "coding_agent",
		Provider:          "claude-code",
		ExternalSessionID: "claude-native-restored",
		ResumeSupported:   true,
	}

	if api.seedCodingAgentRuntimeFromRestoredConversation("new-ui-session", "codex-cli", "", runtime, agent) {
		t.Fatal("expected provider mismatch to skip native resume")
	}
	if handle := mcpagent.SnapshotAgentSession(agent); handle != nil && handle.Provider.NativeSessionID != "" {
		t.Fatalf("unexpected native session ID = %q", handle.Provider.NativeSessionID)
	}
}

// A changed profile definition (prompt, tools, MCP servers) relaunches the CLI
// with the current definition but must keep the same native conversation.
func TestSeedCodingAgentRuntimeFromRestoredConversationResumesAcrossChangedAgentProfile(t *testing.T) {
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "false")
	api := &StreamingAPI{lastAgentProfileKeyBySession: map[string]string{"new-ui-session": "profile-sha256:current"}}
	agent := &mcpagent.Agent{}
	runtime := &ChatHistoryAgentRuntime{
		Kind:              "coding_agent",
		Provider:          "pi-cli",
		ExternalSessionID: "stale-pi-session",
		ResumeSupported:   true,
		AgentProfileKey:   "profile-sha256:old",
	}

	if !api.seedCodingAgentRuntimeFromRestoredConversation("new-ui-session", "pi-cli", "", runtime, agent) {
		t.Fatal("changed agent profile must still resume the same native session")
	}
	if handle := mcpagent.SnapshotAgentSession(agent); handle == nil || handle.Provider.NativeSessionID != "stale-pi-session" {
		t.Fatalf("native session ID = %+v, want stale-pi-session", handle)
	}
}

func TestSeedCodingAgentRuntimeFromRestoredConversationResumesLegacyRuntimeForProfile(t *testing.T) {
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "false")
	api := &StreamingAPI{lastAgentProfileKeyBySession: map[string]string{"new-ui-session": "profile-sha256:current"}}
	agent := &mcpagent.Agent{}
	runtime := &ChatHistoryAgentRuntime{
		Kind:              "coding_agent",
		Provider:          "pi-cli",
		ExternalSessionID: "legacy-pi-session",
		ResumeSupported:   true,
	}

	if !api.seedCodingAgentRuntimeFromRestoredConversation("new-ui-session", "pi-cli", "", runtime, agent) {
		t.Fatal("profile-backed chat must resume its legacy runtime rather than lose the conversation")
	}
}

func TestSeedCodingAgentRuntimeFromRestoredConversationMuseStartsFreshOnChangedProfile(t *testing.T) {
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "false")
	api := &StreamingAPI{lastAgentProfileKeyBySession: map[string]string{"new-ui-session": "profile-sha256:current"}}
	runtime := &ChatHistoryAgentRuntime{Kind: "coding_agent", Provider: "muse-cli", ExternalSessionID: "old-muse", ResumeSupported: true, AgentProfileKey: "profile-sha256:old"}
	if api.seedCodingAgentRuntimeFromRestoredConversation("new-ui-session", "muse-cli", "", runtime, &mcpagent.Agent{}) {
		t.Fatal("muse-cli must start a fresh session when its instructions changed")
	}
}

// Toggling a Crew's native agent tools is a capability change: fresh session.
func TestSeedCodingAgentRuntimeFromRestoredConversationFreshOnAgentToolsModeChange(t *testing.T) {
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "false")
	for _, tc := range []struct {
		saved, current string
		resume         bool
	}{
		{"", "hybrid", false},
		{"mcp_only", "hybrid", false},
		{"hybrid", "mcp_only", false},
		{"hybrid", "hybrid", true},
		{"", "mcp_only", true},
	} {
		api := &StreamingAPI{
			lastAgentProfileKeyBySession: map[string]string{"s": "profile-sha256:current"},
			lastAgentToolsModeBySession:  map[string]string{"s": tc.current},
		}
		runtime := &ChatHistoryAgentRuntime{Kind: "coding_agent", Provider: "claude-code", ExternalSessionID: "native", ResumeSupported: true, AgentProfileKey: "profile-sha256:old", AgentToolsMode: tc.saved}
		if got := api.seedCodingAgentRuntimeFromRestoredConversation("s", "claude-code", "", runtime, &mcpagent.Agent{}); got != tc.resume {
			t.Fatalf("saved=%q current=%q resume=%v, want %v", tc.saved, tc.current, got, tc.resume)
		}
	}
}

func TestSeedCodingAgentRuntimeFromRestoredConversationAcceptsMatchingAgentProfile(t *testing.T) {
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "false")
	api := &StreamingAPI{lastAgentProfileKeyBySession: map[string]string{"new-ui-session": "profile-sha256:current"}}
	agent := &mcpagent.Agent{}
	runtime := &ChatHistoryAgentRuntime{
		Kind:              "coding_agent",
		Provider:          "pi-cli",
		ExternalSessionID: "current-pi-session",
		ResumeSupported:   true,
		AgentProfileKey:   "profile-sha256:current",
	}

	if !api.seedCodingAgentRuntimeFromRestoredConversation("new-ui-session", "pi-cli", "", runtime, agent) {
		t.Fatal("matching agent profile should preserve native resume")
	}
}

func TestAgentProfileAllowsRetainedLiveInputRequiresMatchingPersistedKey(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspace := "_users/user-1/Chats/Work/projects/demo"
	conversationDir := filepath.Join(root, filepath.FromSlash(workspace), "builder", "conversation", "2026-09-15")
	if err := os.MkdirAll(conversationDir, 0o755); err != nil {
		t.Fatal(err)
	}
	conversationPath := filepath.Join(conversationDir, "session-product-chat-conversation.json")
	writeRuntime := func(key string) {
		t.Helper()
		payload := fmt.Sprintf(`{"session_id":"product-chat","runtime":{"kind":"coding_agent","provider":"pi-cli","resume_supported":true,"external_session_id":"native","agent_profile_key":%q}}`, key)
		if err := os.WriteFile(conversationPath, []byte(payload), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	api := &StreamingAPI{lastAgentProfileKeyBySession: map[string]string{"product-chat": "profile-sha256:current"}}
	api.agentCancelFuncs = map[string]context.CancelFunc{"product-chat": func() {}}
	writeRuntime("profile-sha256:old")
	if api.agentProfileAllowsRetainedLiveInput("user-1", "product-chat", workspace, true) {
		t.Fatal("stale product runtime must not accept retained live input")
	}
	api.launchedAgentProfileKeyBySession = map[string]string{"product-chat": "profile-sha256:current"}
	if !api.agentProfileAllowsRetainedLiveInput("user-1", "product-chat", workspace, true) {
		t.Fatal("preceding completed-turn snapshot overrode the current foreground admission")
	}
	api.launchedAgentProfileKeyBySession = nil
	writeRuntime("profile-sha256:current")
	if !api.agentProfileAllowsRetainedLiveInput("user-1", "product-chat", workspace, true) {
		t.Fatal("matching product runtime should accept retained live input")
	}
}

func TestSeedCodingAgentRuntimeFromRestoredConversationRejectsWorkshopModeMismatch(t *testing.T) {
	api := &StreamingAPI{}
	agent := &mcpagent.Agent{}
	// After the workshop merge, "builder" and "optimizer" both normalize to
	// "workshop". The only real workshop-mode mismatch is workshop ↔ run.
	runtime := &ChatHistoryAgentRuntime{
		Kind:              "coding_agent",
		Provider:          "claude-code",
		ExternalSessionID: "claude-native-restored",
		ResumeSupported:   true,
		WorkshopMode:      "workshop",
	}

	if api.seedCodingAgentRuntimeFromRestoredConversation("new-ui-session", "claude-code", "run", runtime, agent) {
		t.Fatal("expected workshop mode mismatch to skip native resume")
	}
	if handle := mcpagent.SnapshotAgentSession(agent); handle != nil && handle.Provider.NativeSessionID != "" {
		t.Fatalf("unexpected native session ID = %q", handle.Provider.NativeSessionID)
	}
}

func TestChatHistoryResumeModesCompatible(t *testing.T) {
	tests := []struct {
		name       string
		restored   string
		current    string
		compatible bool
	}{
		{name: "run resumes run", restored: "run", current: "run", compatible: true},
		{name: "workshop resumes workshop", restored: "builder", current: "workshop", compatible: true},
		{name: "run rejects workshop", restored: "workshop", current: "run", compatible: false},
		{name: "workshop rejects run", restored: "run", current: "workshop", compatible: false},
		{name: "legacy defaults to workshop", restored: "", current: "workshop", compatible: true},
		{name: "legacy cannot enter run", restored: "", current: "run", compatible: false},
		{name: "products have no workflow boundary", restored: "run", current: "", compatible: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := chatHistoryResumeModesCompatible(tt.restored, tt.current); got != tt.compatible {
				t.Fatalf("chatHistoryResumeModesCompatible(%q, %q) = %v, want %v", tt.restored, tt.current, got, tt.compatible)
			}
		})
	}
}

func TestSeedCodingAgentRuntimeFromRestoredConversationRejectsLegacyModeForRun(t *testing.T) {
	api := &StreamingAPI{}
	agent := &mcpagent.Agent{}
	runtime := &ChatHistoryAgentRuntime{
		Kind:              "coding_agent",
		Provider:          "claude-code",
		ExternalSessionID: "legacy-claude-session",
		ResumeSupported:   true,
	}

	if api.seedCodingAgentRuntimeFromRestoredConversation("new-run-session", "claude-code", "run", runtime, agent) {
		t.Fatal("legacy mode-less conversation must not native-resume into Run mode")
	}
}

func TestRestorePersistedConversationHistoryHydratesStableProjectSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	sessionID := "video-studio:project:lumadesk"
	dateDir := filepath.Join(root, "_users", "default", "chat_history", time.Now().Format("2006-01-02"))
	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	conversationPath := filepath.Join(dateDir, chatHistoryConversationFileName(sessionID))
	conversation := `{
  "session_id": "video-studio:project:lumadesk",
  "conversation_history": [
    {"Role": "human", "Parts": [{"Text": "Create an AgentWorks infographic"}]},
    {"Role": "ai", "Parts": [{"Text": "I will prepare the infographic."}]}
  ],
  "runtime": {
    "kind": "coding_agent",
    "provider": "claude-code",
    "external_session_id": "old-claude-session",
    "resume_supported": true,
    "transport": "tmux"
  }
}`
	if err := os.WriteFile(conversationPath, []byte(conversation), 0o600); err != nil {
		t.Fatal(err)
	}

	api := &StreamingAPI{}
	if !api.restorePersistedConversationHistory(sessionID, "default", "", "") {
		t.Fatal("expected durable project transcript to restore into the empty cache")
	}
	if api.restorePersistedConversationHistory(sessionID, "default", "", "") {
		t.Fatal("second restore must not replace the warm in-memory transcript")
	}
	api.conversationMux.RLock()
	history := api.conversationHistory[sessionID]
	api.conversationMux.RUnlock()
	if len(history) != 2 {
		t.Fatalf("restored history length = %d, want 2", len(history))
	}
	if target, ok := api.rememberedRestoredConversationPersistTarget(sessionID); !ok || target.ConversationPath == "" {
		t.Fatal("expected recovered session to keep its original persistence target")
	}
}

func TestRestorePersistedConversationHistoryRejectsWorkshopHistoryForRun(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	sessionID := "legacy-workshop-chat"
	workspacePath := "Workflow/example"
	dateDir := filepath.Join(root, "Workflow", "example", "builder", "conversation", time.Now().Format("2006-01-02"))
	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	conversationPath := filepath.Join(dateDir, chatHistoryConversationFileName(sessionID))
	conversation := `{
  "session_id": "legacy-workshop-chat",
  "workshop_mode": "workshop",
  "conversation_history": [
    {"Role": "human", "Parts": [{"Text": "change the workflow"}]},
    {"Role": "ai", "Parts": [{"Text": "I will edit the plan."}]}
  ],
  "runtime": {
    "kind": "coding_agent",
    "provider": "claude-code",
    "external_session_id": "workshop-native-session",
    "resume_supported": true,
    "workshop_mode": "workshop"
  }
}`
	if err := os.WriteFile(conversationPath, []byte(conversation), 0o600); err != nil {
		t.Fatal(err)
	}

	api := &StreamingAPI{}
	if api.restorePersistedConversationHistory(sessionID, "default", workspacePath, "run") {
		t.Fatal("Workshop transcript must not replay into Run mode")
	}
	api.conversationMux.RLock()
	_, restored := api.conversationHistory[sessionID]
	api.conversationMux.RUnlock()
	if restored {
		t.Fatal("Workshop transcript was loaded into the Run session cache")
	}
	if !api.restorePersistedConversationHistory(sessionID, "default", workspacePath, "workshop") {
		t.Fatal("same-mode Workshop transcript should restore")
	}
}

// The persisted conversation JSON is the canonical record. For a coding CLI the
// provider also keeps a rollout/transcript, but an API-backed chat has nothing
// behind this file, so trimming it on write destroys the only copy.
//
// boundedChatHistoryTail is correct for building prompt context — that is
// lossless, because the file still holds everything — and wrong for deciding
// what to store. This asserts the distinction structurally: the durable write
// path must not call it, while the fallback path must.
func TestDurableChatHistoryIsNotTruncatedOnWrite(t *testing.T) {
	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}

	for _, forbidden := range []string{
		"persistedHistory = boundedChatHistoryTail(",
		"persistedHistoryForDisk = boundedChatHistoryTail(",
	} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("durable write path calls %q — the canonical transcript must not be trimmed on write; "+
				"bound the fallback prompt payload instead", forbidden)
		}
	}

	// The fallback payload IS bounded, with the deliberately smaller limits.
	if !strings.Contains(string(source), "boundedChatHistoryTail(historyForAgent, maxCodingAgentFallbackMessages, maxCodingAgentFallbackBytes)") {
		t.Fatal("the no-resume fallback should still bound what it sends as prompt context")
	}

	// The two limit sets must stay distinct: collapsing them would reintroduce
	// the durable trim by making the fallback bound look safe to reuse.
	if maxCodingAgentFallbackMessages >= maxPersistedChatHistoryMessages {
		t.Fatalf("fallback message limit (%d) must stay well below the persisted ceiling (%d)",
			maxCodingAgentFallbackMessages, maxPersistedChatHistoryMessages)
	}
	if maxCodingAgentFallbackBytes >= maxPersistedChatHistoryBytes {
		t.Fatalf("fallback byte limit (%d) must stay well below the persisted ceiling (%d)",
			maxCodingAgentFallbackBytes, maxPersistedChatHistoryBytes)
	}
	// Production Linux limits one argv string to 128 KiB. Cursor's structured
	// prompt also carries system instructions and the current message, so the
	// replayed history must stay below half that ceiling.
	if maxCodingAgentFallbackBytes >= 64*1024 {
		t.Fatalf("fallback byte limit (%d) is unsafe for Cursor's positional prompt", maxCodingAgentFallbackBytes)
	}
}

func TestBoundedChatHistoryTailKeepsNewestCompleteMessages(t *testing.T) {
	history := make([]llmtypes.MessageContent, 0, 5)
	for _, text := range []string{"one", "two", "three", "four", "five"} {
		history = append(history, llmtypes.MessageContent{
			Role:  llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}},
		})
	}

	tail := boundedChatHistoryTail(history, 3, 1024)
	if len(tail) != 3 {
		t.Fatalf("tail length = %d, want 3", len(tail))
	}
	for i, want := range []string{"three", "four", "five"} {
		part, ok := tail[i].Parts[0].(llmtypes.TextContent)
		if !ok || part.Text != want {
			t.Fatalf("tail[%d] = %#v, want text %q", i, tail[i], want)
		}
	}
}

func TestBoundedChatHistoryTailRetainsNewestOversizedMessage(t *testing.T) {
	history := []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "old"}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: strings.Repeat("x", 4096)}}},
	}

	tail := boundedChatHistoryTail(history, 48, 128)
	if len(tail) != 1 {
		t.Fatalf("tail length = %d, want only the newest oversized message", len(tail))
	}
	part, ok := tail[0].Parts[0].(llmtypes.TextContent)
	if !ok || len(part.Text) != 4096 {
		t.Fatalf("tail did not retain the newest oversized message: %#v", tail[0])
	}
}

func TestAgentProfileRetainedAdmissionUsesLaunchedFingerprint(t *testing.T) {
	api := &StreamingAPI{
		lastAgentProfileKeyBySession:     map[string]string{"crew": "new-linear-scope"},
		launchedAgentProfileKeyBySession: map[string]string{"crew": "new-linear-scope"},
		agentCancelFuncs:                 map[string]context.CancelFunc{"crew": func() {}},
	}
	// Persistence still belongs to the previous completed turn. The foreground
	// agent's actual admission is authoritative until its own turn is saved.
	if !api.agentProfileAllowsRetainedLiveInput("user", "crew", "missing", true) {
		t.Fatal("rebuilt foreground session restarted on unchanged follow-up")
	}
	api.lastAgentProfileKeyBySession["crew"] = "new-notion-scope"
	if api.agentProfileAllowsRetainedLiveInput("user", "crew", "missing", true) {
		t.Fatal("running session accepted a changed MCP scope")
	}
	if api.launchedAgentProfileKeyBySession["crew"] != "new-linear-scope" {
		t.Fatal("new request overwrote launched admission")
	}
}

func TestAgentProfileRuntimeKeepsActualTurnAdmission(t *testing.T) {
	api := &StreamingAPI{lastAgentProfileKeyBySession: map[string]string{"crew": "next-request-scope"}}
	agent := &mcpagent.Agent{}
	api.agentProfileAdmissions.Store(agent, "scope-used-by-this-turn")
	runtime := api.captureChatHistoryAgentRuntime("crew", "muse-cli", "test-model", "Chats/Work/projects/example", agent)
	if runtime.AgentProfileKey != "scope-used-by-this-turn" {
		t.Fatal("canceled turn stamped its old native handle with the next request's admission")
	}
}
