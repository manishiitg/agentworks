package mcpagent

import (
	"errors"
	"fmt"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	claudecode "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
)

func TestSessionIDExtractionFromGenerationInfo(t *testing.T) {
	tests := []struct {
		name       string
		provider   llm.Provider
		additional map[string]interface{}
		wantClaude string
		wantCodex  string
		wantPi     string
	}{
		{
			name:     "claude code session ID extracted",
			provider: llm.ProviderClaudeCode,
			additional: map[string]interface{}{
				"claude_code_session_id": "claude-sess-abc123",
				"provider":               "claude-code",
			},
			wantClaude: "claude-sess-abc123",
		},
		{
			name:     "codex thread ID extracted",
			provider: llm.ProviderCodexCLI,
			additional: map[string]interface{}{
				"codex_thread_id": "019e-codex-thread-id",
				"provider":        "codex-cli",
			},
			wantCodex: "019e-codex-thread-id",
		},
		{
			name:     "pi session ID extracted",
			provider: llm.ProviderPiCLI,
			additional: map[string]interface{}{
				"pi_session_id": "mlp-pi-session-id",
				"provider":      "pi-cli",
			},
			wantPi: "mlp-pi-session-id",
		},
		{
			name:     "empty session ID not stored",
			provider: llm.ProviderClaudeCode,
			additional: map[string]interface{}{
				"claude_code_session_id": "",
			},
			wantClaude: "",
		},
		{
			name:     "wrong type not stored",
			provider: llm.ProviderCodexCLI,
			additional: map[string]interface{}{
				"codex_thread_id": 12345,
			},
			wantCodex: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &Agent{provider: tt.provider}

			resp := &llmtypes.ContentResponse{
				Choices: []*llmtypes.ContentChoice{
					{
						Content: "test response",
						GenerationInfo: &llmtypes.GenerationInfo{
							Additional: tt.additional,
						},
					},
				},
			}

			extractCodingAgentSessionIDs(agent, resp)

			if agent.claudeCodeSessionID != tt.wantClaude {
				t.Errorf("ClaudeCodeSessionID = %q, want %q", agent.claudeCodeSessionID, tt.wantClaude)
			}
			if agent.codexSessionID != tt.wantCodex {
				t.Errorf("CodexSessionID = %q, want %q", agent.codexSessionID, tt.wantCodex)
			}
			if agent.piSessionID != tt.wantPi {
				t.Errorf("PiSessionID = %q, want %q", agent.piSessionID, tt.wantPi)
			}
		})
	}
}

func TestSessionIDResumeOptionsInjected(t *testing.T) {
	tests := []struct {
		name                              string
		provider                          llm.Provider
		modelID                           string
		claudeSessionID                   string
		codexSessionID                    string
		cursorSessionID                   string
		cursorBridgeToolsMode             bool
		piSessionID                       string
		sessionID                         string
		codexPersistentInteractiveSession bool
		wantResumeKey                     string
		wantResumeValue                   string
		wantProjectDirKey                 string
		wantProjectDirValue               string
	}{
		{
			name:            "claude code passes resume session ID",
			provider:        llm.ProviderClaudeCode,
			claudeSessionID: "claude-resume-id",
			wantResumeKey:   claudecode.MetadataKeyResumeSessionID,
			wantResumeValue: "claude-resume-id",
		},
		{
			name:            "codex passes resume thread ID when no persistent interactive",
			provider:        llm.ProviderCodexCLI,
			codexSessionID:  "codex-thread-id",
			wantResumeKey:   codexcli.MetadataKeyResumeSessionID,
			wantResumeValue: "codex-thread-id",
		},
		{
			name:                              "codex passes resume thread ID when persistent interactive enabled",
			provider:                          llm.ProviderCodexCLI,
			modelID:                           "gpt-5.3-codex-spark",
			codexSessionID:                    "codex-thread-id",
			sessionID:                         "app-session-id",
			codexPersistentInteractiveSession: true,
			wantResumeKey:                     codexcli.MetadataKeyResumeSessionID,
			wantResumeValue:                   "codex-thread-id",
		},
		{
			name:            "cursor passes resume session ID outside bridge mode",
			provider:        llm.ProviderCursorCLI,
			cursorSessionID: "cursor-native-id",
			wantResumeKey:   cursorcli.MetadataKeyResumeSessionID,
			wantResumeValue: "cursor-native-id",
		},
		{
			name:                  "cursor bridge mode still passes resume session ID",
			provider:              llm.ProviderCursorCLI,
			cursorSessionID:       "cursor-native-id",
			cursorBridgeToolsMode: true,
			wantResumeKey:         cursorcli.MetadataKeyResumeSessionID,
			wantResumeValue:       "cursor-native-id",
		},
		{
			name:            "pi passes native session ID",
			provider:        llm.ProviderPiCLI,
			piSessionID:     "mlp-pi-resume-id",
			wantResumeKey:   picli.MetadataKeyResumeSessionID,
			wantResumeValue: "mlp-pi-resume-id",
		},
		{
			name:     "claude code no resume when session ID empty",
			provider: llm.ProviderClaudeCode,
		},
		{
			name:     "codex no resume when session ID empty",
			provider: llm.ProviderCodexCLI,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &Agent{
				provider:                          tt.provider,
				modelID:                           tt.modelID,
				sessionID:                         tt.sessionID,
				claudeCodeSessionID:               tt.claudeSessionID,
				codexSessionID:                    tt.codexSessionID,
				cursorSessionID:                   tt.cursorSessionID,
				cursorBridgeToolsMode:             tt.cursorBridgeToolsMode,
				piSessionID:                       tt.piSessionID,
				codexPersistentInteractiveSession: tt.codexPersistentInteractiveSession,
			}

			opts := agent.buildStructuredResumeOptions()
			meta := metadataFromCallOptions(opts)

			if tt.wantResumeKey == "" {
				if len(meta) > 0 {
					t.Fatalf("expected no resume options, got metadata: %v", meta)
				}
				return
			}

			if meta[tt.wantResumeKey] != tt.wantResumeValue {
				t.Fatalf("resume metadata %q = %v, want %q", tt.wantResumeKey, meta[tt.wantResumeKey], tt.wantResumeValue)
			}

			if tt.wantProjectDirKey != "" {
				if meta[tt.wantProjectDirKey] != tt.wantProjectDirValue {
					t.Fatalf("project dir metadata %q = %v, want %q", tt.wantProjectDirKey, meta[tt.wantProjectDirKey], tt.wantProjectDirValue)
				}
			}
		})
	}
}

func TestSessionIDRoundTrip(t *testing.T) {
	providers := []struct {
		name       string
		provider   llm.Provider
		sessionKey string
		resumeKey  string
	}{
		{
			name:       "claude code",
			provider:   llm.ProviderClaudeCode,
			sessionKey: "claude_code_session_id",
			resumeKey:  claudecode.MetadataKeyResumeSessionID,
		},
		{
			name:       "codex cli",
			provider:   llm.ProviderCodexCLI,
			sessionKey: "codex_thread_id",
			resumeKey:  codexcli.MetadataKeyResumeSessionID,
		},
		{
			name:       "cursor cli",
			provider:   llm.ProviderCursorCLI,
			sessionKey: "cursor_session_id",
			resumeKey:  cursorcli.MetadataKeyResumeSessionID,
		},
		{
			name:       "pi cli",
			provider:   llm.ProviderPiCLI,
			sessionKey: "pi_session_id",
			resumeKey:  picli.MetadataKeyResumeSessionID,
		},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			agent := &Agent{provider: p.provider}
			sessionID := "roundtrip-sess-" + p.name

			resp := &llmtypes.ContentResponse{
				Choices: []*llmtypes.ContentChoice{
					{
						Content: "turn 1 response",
						GenerationInfo: &llmtypes.GenerationInfo{
							Additional: map[string]interface{}{
								p.sessionKey: sessionID,
							},
						},
					},
				},
			}

			extractCodingAgentSessionIDs(agent, resp)

			opts := agent.buildStructuredResumeOptions()
			meta := metadataFromCallOptions(opts)

			if meta[p.resumeKey] != sessionID {
				t.Fatalf("round-trip failed: extracted %q but resume option has %v (key=%s)", sessionID, meta[p.resumeKey], p.resumeKey)
			}
		})
	}
}

func TestTypedCodingProviderSessionHandleUpdatesAgent(t *testing.T) {
	agent := &Agent{provider: llm.ProviderClaudeCode, modelID: "old-model"}
	resp := &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{
			{
				Content: "turn 1 response",
				GenerationInfo: &llmtypes.GenerationInfo{
					CodingProviderSessionHandle: &llmtypes.CodingProviderSessionHandle{
						Provider:        "claude-code",
						Transport:       llmtypes.CodingProviderTransportTmux,
						NativeSessionID: "claude-native-1",
						TmuxSession:     "tmux-1",
						WorkingDir:      "/workspace",
						Model:           "claude-sonnet-4-6",
						Status:          llmtypes.CodingProviderSessionStatusIdle,
					},
				},
			},
		},
	}

	extractCodingAgentSessionIDs(agent, resp)

	if agent.claudeCodeSessionID != "claude-native-1" {
		t.Fatalf("ClaudeCodeSessionID = %q, want claude-native-1", agent.claudeCodeSessionID)
	}
	if agent.codingAgentWorkingDir != "/workspace" {
		t.Fatalf("CodingAgentWorkingDir = %q, want /workspace", agent.codingAgentWorkingDir)
	}
	if agent.codingProviderSessionHandle.TmuxSession != "tmux-1" {
		t.Fatalf("typed handle not stored: %#v", agent.codingProviderSessionHandle)
	}
}

func TestAgentSessionHandleApplyRestoresProviderState(t *testing.T) {
	agent := &Agent{}
	handle := &AgentSessionHandle{
		SessionID: "app-session-1",
		Provider: llmtypes.CodingProviderSessionHandle{
			Provider:        "codex-cli",
			Transport:       llmtypes.CodingProviderTransportStructured,
			NativeSessionID: "codex-thread-1",
			WorkingDir:      "/workspace",
			ProjectDirID:    "codex-project-1",
			Model:           "gpt-5.4",
			Status:          llmtypes.CodingProviderSessionStatusIdle,
		},
	}

	agent.applyAgentSessionHandle(handle)

	if agent.sessionID != "app-session-1" {
		t.Fatalf("SessionID = %q, want app-session-1", agent.sessionID)
	}
	if agent.codexSessionID != "codex-thread-1" {
		t.Fatalf("CodexSessionID = %q, want codex-thread-1", agent.codexSessionID)
	}
	if agent.codexProjectDirID != "codex-project-1" {
		t.Fatalf("CodexProjectDirID = %q, want codex-project-1", agent.codexProjectDirID)
	}
	if got := agent.currentAgentSessionHandle(); got == nil || got.Provider.NativeSessionID != "codex-thread-1" {
		t.Fatalf("CurrentAgentSessionHandle = %#v", got)
	}
}

func TestAgentSessionHandleApplyPreservesConfiguredModel(t *testing.T) {
	agent := &Agent{
		provider: llm.ProviderClaudeCode,
		modelID:  "claude-sonnet-5",
	}
	handle := &AgentSessionHandle{
		SessionID: "pulse-session",
		Provider: llmtypes.CodingProviderSessionHandle{
			Provider:        string(llm.ProviderClaudeCode),
			Transport:       llmtypes.CodingProviderTransportTmux,
			NativeSessionID: "builder-conversation",
			Model:           "claude-opus-4-8",
			Status:          llmtypes.CodingProviderSessionStatusIdle,
		},
	}

	agent.applyAgentSessionHandle(handle)

	if agent.modelID != "claude-sonnet-5" {
		t.Fatalf("ModelID = %q, want configured Pulse model", agent.modelID)
	}
	if agent.codingProviderSessionHandle.Model != "claude-sonnet-5" {
		t.Fatalf("stored handle model = %q, want configured Pulse model", agent.codingProviderSessionHandle.Model)
	}
	if agent.claudeCodeSessionID != "builder-conversation" {
		t.Fatalf("ClaudeCodeSessionID = %q, want restored conversation", agent.claudeCodeSessionID)
	}
}

func TestCodingProviderContinuationHandleUsesRequestedModel(t *testing.T) {
	agent := &Agent{
		provider:              llm.ProviderClaudeCode,
		modelID:               "claude-sonnet-5",
		codingAgentWorkingDir: "/tmp/work",
		codingProviderSessionHandle: llmtypes.CodingProviderSessionHandle{
			Provider:        string(llm.ProviderClaudeCode),
			Transport:       llmtypes.CodingProviderTransportTmux,
			NativeSessionID: "builder-conversation",
			Model:           "claude-opus-4-8",
		},
	}

	handle, ok := agent.codingProviderContinuationHandleForModel(llm.ProviderClaudeCode, "claude-sonnet-5")
	if !ok {
		t.Fatal("expected continuation handle")
	}
	if handle.Model != "claude-sonnet-5" {
		t.Fatalf("continuation model = %q, want requested Pulse model", handle.Model)
	}
	// PLAT-856-442: imported Cursor formats keep their compatible runtime;
	// ordinary chats and shared Claude/Codex journals still follow defaults.
	for _, tc := range []struct{ format, transport string }{{"cursor-sdk-sqlite", "sdk"}, {"cursor-cli-sqlite", "tmux"}} {
		cursor := &Agent{provider: llm.ProviderCursorCLI, modelID: "composer-2.5", codingProviderSessionHandle: llmtypes.CodingProviderSessionHandle{Provider: "cursor-cli", NativeSessionID: "native-import", Transport: tc.transport, NativeFormat: tc.format}}
		h := cursor.currentAgentSessionHandle()
		if h.Provider.Transport != tc.transport {
			t.Fatalf("format %s routed to %s", tc.format, h.Provider.Transport)
		}
		// Actual Mac CLI -> SDK-default server failed before the terminal
		// prelaunch guard consulted the imported format (PLAT-856-442).
		if got := cursor.codingAgentLaunchTransport("sdk"); got != tc.transport {
			t.Fatalf("format %s prelaunch routed to %s", tc.format, got)
		}
		cursor.codingAgentTransport = llm.CodingAgentTransportStructured
		if got := cursor.codingAgentLaunchTransport("sdk"); got != "structured" {
			t.Fatalf("explicit structured choice ignored: %s", got)
		}
		cursor.codingAgentTransport = ""
		cursor.updateCodingProviderSessionHandleFromResponse(&llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{GenerationInfo: &llmtypes.GenerationInfo{CodingProviderSessionHandle: &llmtypes.CodingProviderSessionHandle{Provider: "cursor-cli", NativeSessionID: "native-import", Transport: tc.transport}}}}})
		if cursor.currentAgentSessionHandle().Provider.NativeFormat != tc.format {
			t.Fatal("native format lost after a turn")
		}
	}
}

func TestCodingProviderContinuationHandleForModelRequiresMatchingNativeHandle(t *testing.T) {
	agent := &Agent{
		provider:              llm.ProviderClaudeCode,
		modelID:               "claude-sonnet-4-6",
		codingAgentWorkingDir: "/tmp/work",
		codingProviderSessionHandle: llmtypes.CodingProviderSessionHandle{
			Provider:        string(llm.ProviderClaudeCode),
			Transport:       llmtypes.CodingProviderTransportTmux,
			NativeSessionID: "claude-native",
		},
	}

	handle, ok := agent.codingProviderContinuationHandleForModel(llm.ProviderClaudeCode, "claude-sonnet-4-6")
	if !ok {
		t.Fatal("expected continuation handle")
	}
	if handle.WorkingDir != "/tmp/work" {
		t.Fatalf("WorkingDir = %q, want /tmp/work", handle.WorkingDir)
	}
	if handle.Model != "claude-sonnet-4-6" {
		t.Fatalf("Model = %q, want claude-sonnet-4-6", handle.Model)
	}

	if _, ok := agent.codingProviderContinuationHandleForModel(llm.ProviderCodexCLI, "gpt-5.4"); ok {
		t.Fatal("expected provider mismatch to be rejected")
	}

	agent.codingProviderSessionHandle.NativeSessionID = ""
	if _, ok := agent.codingProviderContinuationHandleForModel(llm.ProviderClaudeCode, "claude-sonnet-4-6"); ok {
		t.Fatal("expected missing native session id to be rejected")
	}
}

func TestCodingProviderContinuationHandleAcceptsPiNativeResume(t *testing.T) {
	agent := &Agent{
		provider:              llm.ProviderPiCLI,
		modelID:               "google/gemini-3.8-flash",
		codingAgentWorkingDir: "/tmp/pi-work",
		codingProviderSessionHandle: llmtypes.CodingProviderSessionHandle{
			Provider:        string(llm.ProviderPiCLI),
			Transport:       llmtypes.CodingProviderTransportTmux,
			NativeSessionID: "owner-session",
			TmuxSession:     "mlp-pi-cli-int-owner-session",
			WorkingDir:      "/tmp/pi-work",
		},
	}

	handle, ok := agent.codingProviderContinuationHandleForModel(llm.ProviderPiCLI, "google/gemini-3.8-flash")
	if !ok {
		t.Fatal("expected pi-cli provider-native continuation")
	}
	if handle.NativeSessionID != "owner-session" || handle.WorkingDir != "/tmp/pi-work" {
		t.Fatalf("Pi continuation handle = %#v", handle)
	}
}

func TestMissingCodingProviderNativeSessionError(t *testing.T) {
	missingConversation := errors.New("claude run reported an error result: No conversation found with session ID: dead-session")
	wrapped := fmt.Errorf("all LLMs failed: %w", missingConversation)

	if !isMissingCodingProviderNativeSessionError(llm.ProviderClaudeCode, wrapped) {
		t.Fatal("expected wrapped Claude missing-conversation error to be recoverable")
	}
	if isMissingCodingProviderNativeSessionError(llm.ProviderClaudeCode, errors.New("Claude Code authentication required")) {
		t.Fatal("authentication errors must not trigger a fresh-session retry")
	}
	if isMissingCodingProviderNativeSessionError(llm.ProviderClaudeCode, errors.New("timed out waiting for Claude Code prompt")) {
		t.Fatal("timeout errors must not trigger a fresh-session retry")
	}
	if isMissingCodingProviderNativeSessionError(llm.ProviderCodexCLI, missingConversation) {
		t.Fatal("Claude-specific error must not reset a Codex continuation")
	}
}

func TestClearCodingProviderNativeSessionPreservesAgentWorksState(t *testing.T) {
	agent := &Agent{
		provider:              llm.ProviderClaudeCode,
		modelID:               "claude-sonnet-5",
		claudeCodeSessionID:   "stale-native-id",
		codingAgentWorkingDir: "/workspace/upwork",
		codingProviderSessionHandle: llmtypes.CodingProviderSessionHandle{
			Provider:        string(llm.ProviderClaudeCode),
			Transport:       llmtypes.CodingProviderTransportStructured,
			NativeSessionID: "stale-native-id",
			TmuxSession:     "old-tmux",
			WorkingDir:      "/workspace/upwork",
			Model:           "claude-sonnet-4-6",
			Status:          llmtypes.CodingProviderSessionStatusIdle,
		},
	}

	agent.clearCodingProviderNativeSession(llm.ProviderClaudeCode, "stale-native-id")

	if agent.claudeCodeSessionID != "" {
		t.Fatalf("ClaudeCodeSessionID = %q, want cleared", agent.claudeCodeSessionID)
	}
	if agent.codingProviderSessionHandle.NativeSessionID != "" {
		t.Fatalf("typed NativeSessionID = %q, want cleared", agent.codingProviderSessionHandle.NativeSessionID)
	}
	if agent.codingProviderSessionHandle.TmuxSession != "" {
		t.Fatalf("TmuxSession = %q, want cleared", agent.codingProviderSessionHandle.TmuxSession)
	}
	if agent.codingProviderSessionHandle.WorkingDir != "/workspace/upwork" {
		t.Fatalf("WorkingDir = %q, want preserved", agent.codingProviderSessionHandle.WorkingDir)
	}
	if agent.codingAgentWorkingDir != "/workspace/upwork" {
		t.Fatalf("CodingAgentWorkingDir = %q, want preserved", agent.codingAgentWorkingDir)
	}
	if agent.modelID != "claude-sonnet-5" {
		t.Fatalf("ModelID = %q, want preserved", agent.modelID)
	}
	if _, ok := agent.codingProviderContinuationHandleForModel(llm.ProviderClaudeCode, "claude-sonnet-5"); ok {
		t.Fatal("cleared native session must not be resumed again")
	}
}

func TestClearCodingProviderNativeSessionRejectsMismatchedID(t *testing.T) {
	agent := &Agent{
		provider:            llm.ProviderClaudeCode,
		claudeCodeSessionID: "current-native-id",
		codingProviderSessionHandle: llmtypes.CodingProviderSessionHandle{
			Provider:        string(llm.ProviderClaudeCode),
			NativeSessionID: "current-native-id",
		},
	}

	agent.clearCodingProviderNativeSession(llm.ProviderClaudeCode, "older-native-id")

	if agent.claudeCodeSessionID != "current-native-id" {
		t.Fatalf("ClaudeCodeSessionID = %q, want current id preserved", agent.claudeCodeSessionID)
	}
	if agent.codingProviderSessionHandle.NativeSessionID != "current-native-id" {
		t.Fatalf("typed NativeSessionID = %q, want current id preserved", agent.codingProviderSessionHandle.NativeSessionID)
	}
}

func TestQueuedHumanMessagesAllReachProviderContinuation(t *testing.T) {
	messages := []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeSystem, "system"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "old message"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "old response"),
		llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "new", "message"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "second queued message"),
	}
	got, ok := pendingHumanTextForProviderContinuation(messages)
	if !ok {
		t.Fatal("expected latest human message")
	}
	if got != "new\nmessage\n\nsecond queued message" {
		t.Fatalf("queued input missing or replayed: %q", got)
	}
}

// A stale-native-session retry must never carry the previous attempt's stream
// channel. The failed attempt's adapter already ran `defer close(opts.StreamChan)`,
// so handing that channel to the retry makes its adapter send on a closed channel
// and take the whole process down with "panic: send on closed channel" — observed
// live against claude-code structured transport, where a dead --resume id failed,
// this recovery re-ran the turn, and the scanner goroutine panicked mid-stream.
func TestStaleSessionRetryDropsClosedStreamChannel(t *testing.T) {
	// Exactly the state after a failed first attempt: the adapter closed it.
	streamChan := make(chan llmtypes.StreamChunk, 1)
	close(streamChan)

	baseOpts := []llmtypes.CallOption{llmtypes.WithStreamingChan(streamChan)}

	resolved := &llmtypes.CallOptions{}
	for _, opt := range staleSessionRetryOptions(baseOpts) {
		opt(resolved)
	}
	if resolved.StreamChan != nil {
		t.Fatal("stale-session retry must resolve to a nil StreamChan; reusing the first attempt's closed channel panics the process")
	}

	// The caller's own slice must be left alone — the surrounding turn still
	// owns it (finishStreaming closes/drains it) and must not be mutated here.
	originalResolved := &llmtypes.CallOptions{}
	for _, opt := range baseOpts {
		opt(originalResolved)
	}
	if originalResolved.StreamChan == nil {
		t.Fatal("staleSessionRetryOptions must not mutate the caller's options")
	}
}
