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

// The Costs page opens a project's conversation with the project's plain path; the transcript sits under the person's
// own storage root (PLAT-856-134).
func TestReadChatHistoryConversationFindsOwnedWorkProjectTranscript(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	const workspacePath = "Chats/Work/projects/demo-12345678"
	dir := filepath.Join(root, "_users", "alice", filepath.FromSlash(workspacePath), "builder", "conversation", "2026-09-15")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"session_id":"product-chat-1","user_id":"alice","conversation_history":[],"updated_at":"2026-09-15T05:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, chatHistoryConversationFileName("product-chat-1")), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := ReadChatHistoryConversation("alice", "product-chat-1", workspacePath)
	if err != nil || !strings.Contains(string(data), "product-chat-1") {
		t.Fatalf("read = %q, %v; want the owned project transcript", data, err)
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

// PLAT-872: a chat saved on earlier days kept getting a full new copy in each new day folder (one chat had 6).
// The next day's save must find the chat's newest existing file and keep writing there.
func TestNextDaySaveKeepsTheChatsExistingFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	project := "Workflow/demo"
	name := "session-resumed-chat-conversation.json"
	for _, day := range []string{"2026-10-28", "2026-11-02"} {
		dir := filepath.Join(root, filepath.FromSlash(project), "builder", "conversation", day)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	today := project + "/builder/conversation/2026-11-03/" + name
	existing, ok := newestProjectConversationFile(today)
	if want := project + "/builder/conversation/2026-11-02/" + name; !ok || existing != want {
		t.Fatalf("existing = %q %v, want %q", existing, ok, want)
	}
	if !sameProjectConversationFile(existing, today) {
		t.Fatal("the chat's existing file was not recognised as the same chat")
	}
	if sameProjectConversationFile(project+"/builder/conversation/2026-11-02/session-other-conversation.json", today) {
		t.Fatal("another chat's file was treated as this chat's")
	}
	if _, ok := newestProjectConversationFile(project + "/builder/conversation/2026-11-03/session-new-conversation.json"); ok {
		t.Fatal("a new chat found an existing file")
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

func requireAgentHandle(t *testing.T, agent *mcpagent.Agent) *mcpagent.AgentSessionHandle {
	t.Helper()
	handle := mcpagent.SnapshotAgentSession(agent)
	if handle == nil {
		t.Fatal("expected agent session handle")
	}
	return handle
}

func TestSeedCodingAgentRuntimeFromRestoredConversationAppliesAgentSessionHandle(t *testing.T) {
	// This verifies typed handle restoration independently of the workflow
	// directory boundary, which has dedicated same-directory coverage.
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "false")
	api := &StreamingAPI{}
	agent := &mcpagent.Agent{}
	runtime := &ChatHistoryAgentRuntime{
		Kind:      "coding_agent",
		Provider:  "codex-cli",
		Transport: "tmux",
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
	// PLAT-872: Builder resume used to attempt a terminal launch after this
	// successful seed, then fail because Codex now uses app-server.
	if restoredRuntimeUsesLaunchableTerminalTransport(runtime) {
		t.Fatal("restored Codex native handle must continue without terminal prelaunch")
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

// The owner's Crew chat history is kept in the Crew's Builder folder; another person's Run chats never enter it.
func TestCrewChatHistoryPathFollowsOwnership(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	crew := registerTestCrew(t, "demo-12345678", "alice", true)
	at := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	got, ok := workProjectChatHistoryConversationPath("alice", crew, "chat-1", at)
	want := crew + "/builder/conversation/2026-09-14/session-chat-1-conversation.json"
	if !ok || !strings.HasSuffix(got, want) {
		t.Fatalf("owner path = %q ok=%v, want it to end with %q", got, ok, want)
	}
	if got, ok := workProjectChatHistoryConversationPath("bob", crew, "chat-1", at); ok || got != "" {
		t.Fatalf("a Run person's chat entered Builder storage: %q", got)
	}
}
