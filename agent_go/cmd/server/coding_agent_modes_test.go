package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"

	pkgevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestCodingAgentPersistentInteractiveFlags(t *testing.T) {
	tests := []struct {
		name            string
		provider        string
		allowPersistent bool
		structured      bool
		wantClaudeCode  bool
		wantCodexCLI    bool
		wantCursorCLI   bool
		wantPiCLI       bool
		wantMuseCLI     bool
		wantAgyCLI      bool
	}{
		{
			name:            "claude code chat gets persistent tmux",
			provider:        string(llm.ProviderClaudeCode),
			allowPersistent: true,
			wantClaudeCode:  true,
		},
		{
			name:            "codex chat gets persistent tmux",
			provider:        string(llm.ProviderCodexCLI),
			allowPersistent: true,
			wantCodexCLI:    true,
		},
		{
			name:            "cursor chat uses structured transport",
			provider:        string(llm.ProviderCursorCLI),
			allowPersistent: true,
			structured:      true,
		},
		{
			name:            "cursor workflow builder gets persistent tmux",
			provider:        string(llm.ProviderCursorCLI),
			allowPersistent: true,
			wantCursorCLI:   true,
		},
		{
			name:            "pi chat gets persistent tmux",
			provider:        string(llm.ProviderPiCLI),
			allowPersistent: true,
			wantPiCLI:       true,
		},
		{
			name:            "muse chat gets persistent tmux",
			provider:        string(llm.ProviderMuseCLI),
			allowPersistent: true,
			wantMuseCLI:     true,
		},
		{
			name:            "muse structured chat never persists",
			provider:        string(llm.ProviderMuseCLI),
			allowPersistent: true,
			structured:      true,
		},
		{
			name:            "agy chat gets persistent tmux",
			provider:        string(llm.ProviderAgyCLI),
			allowPersistent: true,
			wantAgyCLI:      true,
		},
		{
			name:            "agy structured chat never persists",
			provider:        string(llm.ProviderAgyCLI),
			allowPersistent: true,
			structured:      true,
		},
		{
			name:            "non coding provider never gets tmux",
			provider:        string(llm.ProviderOpenAI),
			allowPersistent: true,
		},
		{
			name:     "noninteractive coding agent never persists",
			provider: string(llm.ProviderClaudeCode),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotClaudeCode, gotCodexCLI, gotCursorCLI, gotPiCLI, gotMuseCLI, gotAgyCLI := codingAgentPersistentInteractiveFlags(tt.provider, tt.allowPersistent, tt.structured)
			if gotClaudeCode != tt.wantClaudeCode || gotCodexCLI != tt.wantCodexCLI || gotCursorCLI != tt.wantCursorCLI || gotPiCLI != tt.wantPiCLI || gotMuseCLI != tt.wantMuseCLI || gotAgyCLI != tt.wantAgyCLI {
				t.Fatalf("flags = (%v, %v, %v, %v, %v, %v), want (%v, %v, %v, %v, %v, %v)", gotClaudeCode, gotCodexCLI, gotCursorCLI, gotPiCLI, gotMuseCLI, gotAgyCLI, tt.wantClaudeCode, tt.wantCodexCLI, tt.wantCursorCLI, tt.wantPiCLI, tt.wantMuseCLI, tt.wantAgyCLI)
			}
		})
	}
}

func TestCodingAgentPersistentInteractiveFlagsCoverTmuxContracts(t *testing.T) {
	for _, contract := range llm.CodingAgentProviderContracts() {
		if contract.Transport != llm.CodingAgentTransportTmux {
			continue
		}
		t.Run(string(contract.Provider), func(t *testing.T) {
			gotClaudeCode, gotCodexCLI, gotCursorCLI, gotPiCLI, gotMuseCLI, gotAgyCLI := codingAgentPersistentInteractiveFlags(string(contract.Provider), true, false)
			count := 0
			for _, enabled := range []bool{gotClaudeCode, gotCodexCLI, gotCursorCLI, gotPiCLI, gotMuseCLI, gotAgyCLI} {
				if enabled {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("provider %q enables %d persistent flags, want exactly one", contract.Provider, count)
			}
		})
	}
}

func TestCodingAgentUsesStructuredTransportForChat(t *testing.T) {
	tests := []struct {
		name           string
		provider       string
		interactive    bool
		wantStructured bool
	}{
		{name: "Cursor interactive chat uses tmux", provider: "cursor-cli", interactive: true},
		{name: "Muse interactive chat uses tmux", provider: "muse-cli", interactive: true},
		{name: "Claude interactive chat uses tmux", provider: "claude-code", interactive: true},
		{name: "Codex interactive chat uses tmux", provider: "codex-cli", interactive: true},
		{name: "Pi interactive chat uses tmux", provider: "pi-cli", interactive: true},
		{name: "non-retained Cursor uses structured JSON", provider: "cursor-cli", wantStructured: true},
		{name: "non-retained Claude uses structured JSON", provider: "claude-code", wantStructured: true},
		{name: "API provider is not a structured coding CLI", provider: "openai"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := codingAgentUsesStructuredTransportForChat(tc.provider, tc.interactive); got != tc.wantStructured {
				t.Fatalf("structured = %v, want %v", got, tc.wantStructured)
			}
		})
	}
}

func TestCodingAgentRequestAllowsPersistentInteractive(t *testing.T) {
	tests := []struct {
		name string
		req  *QueryRequest
		want bool
	}{
		{name: "ordinary user chat", req: &QueryRequest{AgentMode: "multi-agent"}, want: true},
		{name: "ordinary workflow builder chat", req: &QueryRequest{AgentMode: "workflow_phase", PhaseID: "workflow-builder"}, want: true},
		{name: "scheduled turn uses tmux", req: &QueryRequest{TriggeredBy: "cron"}, want: true},
		{name: "scheduled workflow builder phase uses tmux", req: &QueryRequest{AgentMode: "workflow_phase", PhaseID: "workflow-builder", TriggeredBy: "cron"}, want: true},
		{name: "webhook trigger uses tmux", req: &QueryRequest{TriggeredBy: "webhook"}, want: true},
		{name: "bot turn uses tmux", req: &QueryRequest{TriggeredBy: "bot"}, want: true},
		{name: "background child uses tmux", req: &QueryRequest{ParentSessionID: "parent-1"}, want: true},
		{name: "auto notification uses tmux", req: &QueryRequest{IsAutoNotification: true}, want: true},
		{name: "typed runtime stage stays structured", req: &QueryRequest{SessionKind: "pulse_reviewer"}},
		{name: "nil request", req: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codingAgentRequestAllowsPersistentInteractive(tt.req); got != tt.want {
				t.Fatalf("allows persistent = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCodingAgentClaudeCodeChatTransport(t *testing.T) {
	if got := codingAgentClaudeCodeChatTransport(string(llm.ProviderClaudeCode)); got != llm.ClaudeCodeTransportTmux {
		t.Fatalf("claude-code chat transport = %q, want %q", got, llm.ClaudeCodeTransportTmux)
	}
	if got := codingAgentClaudeCodeChatTransport(string(llm.ProviderCodexCLI)); got != "" {
		t.Fatalf("non-Claude chat transport = %q, want empty", got)
	}
}

func TestCodingAgentWorkspaceWorkingDirUsesWorkspaceDocsRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)

	got := codingAgentWorkspaceWorkingDir("_users/alice/Chats")
	want := filepath.Join(root, "_users", "alice", "Chats")
	if got != want {
		t.Fatalf("working dir = %q, want %q", got, want)
	}
}

func TestDelegatedCodingAgentRuntimeFolderIsPerAgent(t *testing.T) {
	got := delegatedCodingAgentRuntimeFolder("alice", "Save Memory/agent #1")
	want := "_users/alice/Chats/.agents/Save-Memory-agent-1"
	if got != want {
		t.Fatalf("delegated runtime folder = %q, want %q", got, want)
	}

	got = delegatedCodingAgentRuntimeFolder("../bad-user", "worker")
	want = "_users/default/Chats/.agents/worker"
	if got != want {
		t.Fatalf("delegated runtime folder with unsafe user = %q, want %q", got, want)
	}
}

func TestTopLevelTierModelDoesNotOverrideExplicitChatLLM(t *testing.T) {
	t.Setenv("WORKSPACE_API_URL", "http://127.0.0.1:9999")
	req := QueryRequest{
		Provider: "codex-cli",
		ModelID:  "high",
		LLMConfig: &orchestrator.LLMConfig{
			Primary: orchestrator.LLMModel{
				Provider: "codex-cli",
				ModelID:  "high",
			},
		},
		DelegationTierConfig: &virtualtools.DelegationTierConfig{
			High: &virtualtools.TierModel{
				Provider: "claude-code",
				ModelID:  "claude-sonnet-4-6",
			},
		},
	}

	gotProvider, gotModel, applied := applyTopLevelDelegationModel(context.Background(), req, "codex-cli", "high")
	if applied {
		t.Fatal("tier model was applied despite an explicit chat LLM selection")
	}
	if gotProvider != "codex-cli" || gotModel != "high" {
		t.Fatalf("resolved chat LLM = %s/%s, want codex-cli/high", gotProvider, gotModel)
	}
}

func TestTopLevelTierModelAppliesWhenChatLLMIsMissing(t *testing.T) {
	t.Setenv("WORKSPACE_API_URL", "http://127.0.0.1:9999")
	req := QueryRequest{
		DelegationTierConfig: &virtualtools.DelegationTierConfig{
			High: &virtualtools.TierModel{
				Provider: "claude-code",
				ModelID:  "claude-sonnet-4-6",
			},
		},
	}

	gotProvider, gotModel, applied := applyTopLevelDelegationModel(context.Background(), req, "", "")
	if !applied {
		t.Fatal("tier model was not applied for a request with no chat LLM selection")
	}
	if gotProvider != "claude-code" || gotModel != "claude-sonnet-4-6" {
		t.Fatalf("resolved chat LLM = %s/%s, want claude-code/claude-sonnet-4-6", gotProvider, gotModel)
	}
}

func TestProviderProfileOverridesStaleExplicitChatLLM(t *testing.T) {
	t.Setenv("WORKSPACE_API_URL", "http://127.0.0.1:9999")
	defaults, ok := llmproviders.GetCodingAgentDefaultTierModels(llmproviders.ProviderCodexCLI)
	if !ok {
		t.Fatal("expected Codex CLI coding-agent defaults")
	}
	req := QueryRequest{
		Provider: "openrouter",
		ModelID:  "grok-1",
		LLMConfig: &orchestrator.LLMConfig{
			Primary: orchestrator.LLMModel{
				Provider: "openrouter",
				ModelID:  "grok-1",
			},
		},
		DelegationTierConfig: &virtualtools.DelegationTierConfig{
			SchemaVersion: 2,
			Mode:          "provider_profile",
			Provider:      "codex-cli",
		},
	}

	gotProvider, gotModel, applied := applyTopLevelDelegationModel(
		context.Background(), req, "openrouter", "grok-1",
	)
	if !applied {
		t.Fatal("provider profile did not override the stale explicit chat LLM")
	}
	if gotProvider != defaults.Builder.Provider || gotModel != defaults.Builder.ModelID {
		t.Fatalf("resolved chat LLM = %s/%s, want registered Codex Builder default %s/%s", gotProvider, gotModel, defaults.Builder.Provider, defaults.Builder.ModelID)
	}
}

func TestRecordLiveCodingAgentUserMessageCapturesVisibleEvent(t *testing.T) {
	tests := []struct {
		name     string
		provider llm.Provider
	}{
		{name: "claude code", provider: llm.ProviderClaudeCode},
		{name: "codex cli", provider: llm.ProviderCodexCLI},
		{name: "cursor cli", provider: llm.ProviderCursorCLI},
		{name: "pi cli", provider: llm.ProviderPiCLI},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := internalevents.NewEventStore(10)
			defer store.Stop()

			sessionID := "live-coding-session-" + string(tt.provider)
			// Durable-ack providers record the row once the CLI confirms it.
			api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(), eventStore: store,
				internalDurableAckHandler: func(context.Context, llmproviders.Provider, string, string) (llmtypes.DurableAck, error) {
					return llmtypes.DurableAck{Outcome: llmtypes.DurableAckConfirmed}, nil
				},
			}
			sub := store.Subscribe(sessionID)
			defer store.Unsubscribe(sessionID, sub)

			api.recordLiveCodingAgentUserMessage(sessionID, "show exact sequence item", string(tt.provider), "test-message-id", "sent_to_cli", "")

			var delivered internalevents.Event
			select {
			case delivered = <-sub.Ch:
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for live coding user_message event")
			}
			assertLiveCodingUserMessageEvent(t, delivered, sessionID, string(tt.provider))

			// Count only user_message rows: recording a sent_to_cli send
			// also starts the durable watch, which asynchronously records
			// its own live_input_confirmed event (covered in
			// live_input_durable_test.go). That event may or may not be
			// present yet; this test's subject is the user row.
			raw := filterEventsByType(store.GetAllEventsRaw(sessionID), string(pkgevents.UserMessage))
			if len(raw) != 1 {
				t.Fatalf("raw user_message count = %d, want 1", len(raw))
			}
			assertLiveCodingUserMessageEvent(t, raw[0], sessionID, string(tt.provider))

			poll := store.GetEvents(sessionID, internalevents.GetEventsOptions{SinceIndex: -1})
			if got := filterEventsByType(poll.Events, string(pkgevents.UserMessage)); len(got) != 1 {
				t.Fatalf("poll user_message count = %d, want 1", len(got))
			}
			assertLiveCodingUserMessageEvent(t, poll.Events[0], sessionID, string(tt.provider))
		})
	}
}

func TestHandleLiveInputMessageRoutesThroughAgentDelivery(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	sessionID := "queued-delivery-session"
	runningAgent := testCodingAgent(llm.ProviderOpenAI, "gpt-5")
	delivered := 0
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		eventStore:       store,
		runningAgents:    map[string]*mcpagent.Agent{sessionID: runningAgent},
		runningAgentsMux: sync.RWMutex{},
		agentCancelFuncs: map[string]context.CancelFunc{sessionID: func() {}},
		agentCancelMux:   sync.RWMutex{},
		// A turn in flight takes the message into its queue (an idle agent
		// refuses it; see TestHandleLiveInputMessageIdleAgentRefusalIsDefinite).
		internalUserMessageDeliveryHandler: func(_ context.Context, gotAgent *mcpagent.Agent, req mcpagent.UserMessageDeliveryRequest) (mcpagent.UserMessageDeliveryResult, error) {
			delivered++
			if gotAgent != runningAgent || req.Message != "send this through delivery" {
				t.Fatalf("delivery = agent %p message %q, want the running agent and the message", gotAgent, req.Message)
			}
			return mcpagent.UserMessageDeliveryResult{DeliveryStatus: mcpagent.UserMessageDeliveryStatusQueuedForInjection}, nil
		},
	}

	body := bytes.NewBufferString(`{"message":"send this through delivery"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/live-input", body)
	req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
	rr := httptest.NewRecorder()

	api.handleLiveInputMessage(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}

	var response LiveInputResponse
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.DeliveryStatus != "queued_for_injection" {
		t.Fatalf("delivery_status = %q, want queued_for_injection", response.DeliveryStatus)
	}
	if delivered != 1 {
		t.Fatalf("delivered %d times, want once through agent delivery", delivered)
	}
}

// An agent with no turn running refuses a live message (nothing delivered).
// That is definite: with no previous request to start a turn from, the
// answer is a plain 409 — never "delivery uncertain", which would block
// sending the message again.
func TestHandleLiveInputMessageIdleAgentRefusalIsDefinite(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()
	sessionID := "idle-agent-session"
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		eventStore:       store,
		runningAgents:    map[string]*mcpagent.Agent{sessionID: testCodingAgent(llm.ProviderOpenAI, "gpt-5")},
		runningAgentsMux: sync.RWMutex{},
		agentCancelFuncs: map[string]context.CancelFunc{sessionID: func() {}},
		agentCancelMux:   sync.RWMutex{},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/live-input", bytes.NewBufferString(`{"message":"hello again"}`))
	req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
	rr := httptest.NewRecorder()
	api.handleLiveInputMessage(rr, req)
	if rr.Code != http.StatusConflict || strings.Contains(rr.Body.String(), "delivery_uncertain") {
		t.Fatalf("idle agent refusal = %d %s, want a definite 409", rr.Code, rr.Body.String())
	}
}

func TestHandleLiveInputMessageBusyCodingAgentDeliversExactlyOnce(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	const sessionID = "busy-codex-live-input"
	const message = "apply this follow-up while processing"
	runningAgent := testCodingAgent(llm.ProviderCodexCLI, "gpt-5.6-sol")
	var deliveryCalls atomic.Int32
	var nextTurnCalls atomic.Int32
	cancelCalled := false
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		eventStore:       store,
		runningAgents:    map[string]*mcpagent.Agent{sessionID: runningAgent},
		runningAgentsMux: sync.RWMutex{},
		agentCancelFuncs: map[string]context.CancelFunc{sessionID: func() { cancelCalled = true }},
		agentCancelMux:   sync.RWMutex{},
		internalUserMessageDeliveryHandler: func(_ context.Context, gotAgent *mcpagent.Agent, req mcpagent.UserMessageDeliveryRequest) (mcpagent.UserMessageDeliveryResult, error) {
			deliveryCalls.Add(1)
			if gotAgent != runningAgent {
				t.Fatalf("delivery agent = %p, want retained agent %p", gotAgent, runningAgent)
			}
			if req.SessionID != sessionID || req.Message != message || req.Intent != mcpagent.UserMessageDeliveryIntentLiveInput {
				t.Fatalf("delivery request = %#v", req)
			}
			return mcpagent.UserMessageDeliveryResult{
				Provider:       llm.ProviderCodexCLI,
				DeliveryStatus: mcpagent.UserMessageDeliveryStatusSentToCLI,
				Transport:      llm.CodingAgentTransportTmux,
			}, nil
		},
		internalQueryHandler: func(http.ResponseWriter, *http.Request) {
			nextTurnCalls.Add(1)
		},
		internalDurableAckHandler: func(_ context.Context, provider llmproviders.Provider, owner, msg string) (llmtypes.DurableAck, error) {
			if provider != llmproviders.ProviderCodexCLI || owner != sessionID || msg != message {
				t.Errorf("durable watch request = (%s %s %q)", provider, owner, msg)
			}
			return llmtypes.DurableAck{Outcome: llmtypes.DurableAckConfirmed, Latency: 300 * time.Millisecond, ProofSource: "rollout-test.jsonl"}, nil
		},
	}

	body := bytes.NewBufferString(`{"message":"` + message + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/live-input", body)
	req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
	rr := httptest.NewRecorder()
	started := time.Now()

	api.handleLiveInputMessage(rr, req)
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("busy live-input acknowledgement took %s, want under 1s", elapsed)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var response LiveInputResponse
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.DeliveryStatus != "sent_to_cli" || response.MessageID == "" {
		t.Fatalf("response = %#v, want confirmed CLI delivery", response)
	}
	if got := deliveryCalls.Load(); got != 1 {
		t.Fatalf("delivery calls = %d, want exactly 1", got)
	}
	if got := nextTurnCalls.Load(); got != 0 {
		t.Fatalf("next-turn calls = %d, want 0 after confirmed live delivery", got)
	}
	if cancelCalled || !api.hasActiveTurnCancel(sessionID) {
		t.Fatal("confirmed live delivery must keep the current foreground turn active")
	}

	events := store.GetAllEventsRaw(sessionID)
	if len(events) < 1 {
		t.Fatalf("recorded events = %d, want at least the user_message", len(events))
	}
	payload, ok := events[0].Data.Data.(*pkgevents.UserMessageEvent)
	if !ok {
		t.Fatalf("event payload = %T, want *UserMessageEvent", events[0].Data.Data)
	}
	if payload.Content != message || payload.Metadata["message_id"] != response.MessageID || payload.Metadata["delivery_status"] != "sent_to_cli" {
		t.Fatalf("recorded live-input payload = %#v", payload)
	}

	// The durability half of the receipt arrives async: the watcher
	// confirms the same message_id without a second delivery.
	deadline := time.Now().Add(5 * time.Second)
	for {
		events = store.GetAllEventsRaw(sessionID)
		if len(events) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recorded events = %d, want user_message + live_input_confirmed", len(events))
		}
		time.Sleep(10 * time.Millisecond)
	}
	confirm, ok := events[1].Data.Data.(*pkgevents.LiveInputConfirmedEvent)
	if !ok {
		t.Fatalf("second event payload = %T, want *LiveInputConfirmedEvent", events[1].Data.Data)
	}
	if events[1].ID != response.MessageID+":confirmed" || confirm.MessageID != response.MessageID || confirm.Outcome != "confirmed" {
		t.Fatalf("confirm payload = %#v", confirm)
	}
	if got := deliveryCalls.Load(); got != 1 {
		t.Fatalf("delivery calls after confirm = %d, want exactly 1", got)
	}
}

func TestSyntheticTurnRunningAgentRegistrationDeliversLiveInputAndPreservesReplacement(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	const sessionID = "synthetic-codex-live-input"
	const message = "apply this while the synthetic turn is processing"
	syntheticAgent := testCodingAgent(llm.ProviderCodexCLI, "gpt-5.6-sol")
	var deliveryCalls atomic.Int32
	var nextTurnCalls atomic.Int32

	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		eventStore:       store,
		runningAgents:    map[string]*mcpagent.Agent{},
		runningAgentsMux: sync.RWMutex{},
		// executeSyntheticTurn installs this cancel handle before registering
		// the stored agent, so live-input treats it as the active turn.
		agentCancelFuncs: map[string]context.CancelFunc{sessionID: func() {}},
		agentCancelMux:   sync.RWMutex{},
		internalUserMessageDeliveryHandler: func(_ context.Context, gotAgent *mcpagent.Agent, req mcpagent.UserMessageDeliveryRequest) (mcpagent.UserMessageDeliveryResult, error) {
			deliveryCalls.Add(1)
			if gotAgent != syntheticAgent {
				t.Fatalf("delivery agent = %p, want synthetic agent %p", gotAgent, syntheticAgent)
			}
			if req.SessionID != sessionID || req.Message != message || req.Intent != mcpagent.UserMessageDeliveryIntentLiveInput {
				t.Fatalf("delivery request = %#v", req)
			}
			return mcpagent.UserMessageDeliveryResult{
				Provider:       llm.ProviderCodexCLI,
				DeliveryStatus: mcpagent.UserMessageDeliveryStatusSentToCLI,
				Transport:      llm.CodingAgentTransportTmux,
			}, nil
		},
		internalQueryHandler: func(http.ResponseWriter, *http.Request) {
			nextTurnCalls.Add(1)
		},
	}

	cleanupSyntheticRegistration := api.registerRunningAgentForTurn(sessionID, syntheticAgent)
	body := bytes.NewBufferString(`{"message":"` + message + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/live-input", body)
	req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
	rr := httptest.NewRecorder()

	api.handleLiveInputMessage(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var response LiveInputResponse
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.DeliveryStatus != "sent_to_cli" {
		t.Fatalf("delivery_status = %q, want sent_to_cli", response.DeliveryStatus)
	}
	if got := deliveryCalls.Load(); got != 1 {
		t.Fatalf("delivery calls = %d, want exactly 1", got)
	}
	if got := nextTurnCalls.Load(); got != 0 {
		t.Fatalf("next-turn calls = %d, want 0", got)
	}

	// Reproduce the completion-boundary race: a newer turn replaces the map
	// entry before the synthetic turn's deferred cleanup runs. Old cleanup must
	// leave the newer agent registered.
	newerAgent := testCodingAgent(llm.ProviderCodexCLI, "gpt-5.6-sol")
	api.runningAgentsMux.Lock()
	api.runningAgents[sessionID] = newerAgent
	api.runningAgentsMux.Unlock()
	cleanupSyntheticRegistration()

	api.runningAgentsMux.RLock()
	gotAgent := api.runningAgents[sessionID]
	api.runningAgentsMux.RUnlock()
	if gotAgent != newerAgent {
		t.Fatalf("synthetic cleanup removed/replaced newer agent: got %p, want %p", gotAgent, newerAgent)
	}
}

func TestHandleLiveInputMessageCompletionBoundaryChoosesExactlyOneRoute(t *testing.T) {
	t.Run("completion during confirmed delivery stays live-only", func(t *testing.T) {
		store := internalevents.NewEventStore(10)
		defer store.Stop()

		const sessionID = "completion-during-delivery"
		runningAgent := testCodingAgent(llm.ProviderCodexCLI, "gpt-5.6-sol")
		var deliveryCalls atomic.Int32
		var nextTurnCalls atomic.Int32
		var api *StreamingAPI
		api = &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
			eventStore:       store,
			runningAgents:    map[string]*mcpagent.Agent{sessionID: runningAgent},
			runningAgentsMux: sync.RWMutex{},
			agentCancelFuncs: map[string]context.CancelFunc{sessionID: func() {}},
			agentCancelMux:   sync.RWMutex{},
			internalUserMessageDeliveryHandler: func(_ context.Context, _ *mcpagent.Agent, _ mcpagent.UserMessageDeliveryRequest) (mcpagent.UserMessageDeliveryResult, error) {
				deliveryCalls.Add(1)
				api.agentCancelMux.Lock()
				delete(api.agentCancelFuncs, sessionID)
				api.agentCancelMux.Unlock()
				return mcpagent.UserMessageDeliveryResult{Provider: llm.ProviderCodexCLI, DeliveryStatus: mcpagent.UserMessageDeliveryStatusSentToCLI}, nil
			},
			internalQueryHandler: func(http.ResponseWriter, *http.Request) { nextTurnCalls.Add(1) },
			internalDurableAckHandler: func(context.Context, llmproviders.Provider, string, string) (llmtypes.DurableAck, error) {
				return llmtypes.DurableAck{Outcome: llmtypes.DurableAckConfirmed}, nil
			},
		}

		body := bytes.NewBufferString(`{"message":"boundary follow-up"}`)
		req := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/live-input", body), map[string]string{"session_id": sessionID})
		rr := httptest.NewRecorder()
		api.handleLiveInputMessage(rr, req)

		if rr.Code != http.StatusOK || deliveryCalls.Load() != 1 || nextTurnCalls.Load() != 0 {
			t.Fatalf("status=%d delivery=%d next_turn=%d body=%s", rr.Code, deliveryCalls.Load(), nextTurnCalls.Load(), rr.Body.String())
		}
		// Live delivery plus its async durability receipt; the receipt
		// is not a route, so the boundary contract (live XOR next
		// turn) still holds with delivery==1 and next_turn==0.
		deadline := time.Now().Add(5 * time.Second)
		for {
			if got := len(store.GetAllEventsRaw(sessionID)); got == 2 {
				break
			} else if time.Now().After(deadline) {
				t.Fatalf("recorded events = %d, want user_message + live_input_confirmed", got)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if events := store.GetAllEventsRaw(sessionID); events[1].Type != string(pkgevents.LiveInputConfirmed) {
			t.Fatalf("second event type = %q, want live_input_confirmed", events[1].Type)
		}
	})

	t.Run("lost active session never also starts a fallback", func(t *testing.T) {
		const sessionID = "completion-loses-delivery"
		runningAgent := testCodingAgent(llm.ProviderCodexCLI, "gpt-5.6-sol")
		var deliveryCalls atomic.Int32
		var nextTurnCalls atomic.Int32
		var api *StreamingAPI
		api = &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
			runningAgents:    map[string]*mcpagent.Agent{sessionID: runningAgent},
			runningAgentsMux: sync.RWMutex{},
			agentCancelFuncs: map[string]context.CancelFunc{sessionID: func() {}},
			agentCancelMux:   sync.RWMutex{},
			internalUserMessageDeliveryHandler: func(_ context.Context, _ *mcpagent.Agent, _ mcpagent.UserMessageDeliveryRequest) (mcpagent.UserMessageDeliveryResult, error) {
				deliveryCalls.Add(1)
				api.agentCancelMux.Lock()
				delete(api.agentCancelFuncs, sessionID)
				api.agentCancelMux.Unlock()
				return mcpagent.UserMessageDeliveryResult{}, errors.New("tmux session completed during delivery")
			},
			internalQueryHandler: func(http.ResponseWriter, *http.Request) { nextTurnCalls.Add(1) },
		}

		body := bytes.NewBufferString(`{"message":"boundary follow-up"}`)
		req := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/live-input", body), map[string]string{"session_id": sessionID})
		rr := httptest.NewRecorder()
		api.handleLiveInputMessage(rr, req)

		if rr.Code != http.StatusConflict || deliveryCalls.Load() != 1 || nextTurnCalls.Load() != 0 {
			t.Fatalf("status=%d delivery=%d next_turn=%d body=%s", rr.Code, deliveryCalls.Load(), nextTurnCalls.Load(), rr.Body.String())
		}
	})

	t.Run("completion before request queues only a next turn", func(t *testing.T) {
		const sessionID = "completed-before-delivery"
		runningAgent := testCodingAgent(llm.ProviderCodexCLI, "gpt-5.6-sol")
		var deliveryCalls atomic.Int32
		var nextTurnCalls atomic.Int32
		nextTurnDone := make(chan struct{})
		api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
			runningAgents:    map[string]*mcpagent.Agent{sessionID: runningAgent},
			runningAgentsMux: sync.RWMutex{},
			agentCancelFuncs: map[string]context.CancelFunc{},
			agentCancelMux:   sync.RWMutex{},
			lastQueryRequests: map[string]QueryRequest{
				sessionID: {AgentMode: "multi-agent", Provider: string(llm.ProviderCodexCLI), ModelID: "gpt-5.6-sol"},
			},
			internalUserMessageDeliveryHandler: func(_ context.Context, _ *mcpagent.Agent, _ mcpagent.UserMessageDeliveryRequest) (mcpagent.UserMessageDeliveryResult, error) {
				deliveryCalls.Add(1)
				return mcpagent.UserMessageDeliveryResult{}, nil
			},
			internalQueryHandler: func(w http.ResponseWriter, _ *http.Request) {
				nextTurnCalls.Add(1)
				_ = json.NewEncoder(w).Encode(QueryResponse{QueryID: "boundary-next-turn"})
				close(nextTurnDone)
			},
		}

		body := bytes.NewBufferString(`{"message":"boundary follow-up"}`)
		req := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/live-input", body), map[string]string{"session_id": sessionID})
		rr := httptest.NewRecorder()
		api.handleLiveInputMessage(rr, req)
		select {
		case <-nextTurnDone:
		case <-time.After(time.Second):
			t.Fatal("queued next turn did not start")
		}

		if rr.Code != http.StatusOK || deliveryCalls.Load() != 0 || nextTurnCalls.Load() != 1 {
			t.Fatalf("status=%d delivery=%d next_turn=%d body=%s", rr.Code, deliveryCalls.Load(), nextTurnCalls.Load(), rr.Body.String())
		}
		var response LiveInputResponse
		if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if response.DeliveryStatus != "next_turn_started" {
			t.Fatalf("delivery status = %q, want next_turn_started", response.DeliveryStatus)
		}
	})
}

func TestHandleLiveInputMessageRejectsStaleRetainedCodingAgent(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	sessionID := "stale-claude-session"
	runningAgent := testCodingAgent(llm.ProviderClaudeCode, "claude-sonnet-4-6")
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		eventStore:       store,
		terminalStore:    terminals.NewStore(),
		runningAgents:    map[string]*mcpagent.Agent{sessionID: runningAgent},
		runningAgentsMux: sync.RWMutex{},
		sessionBusy:      map[string]bool{sessionID: true},
		sessionBusySince: map[string]time.Time{sessionID: time.Now().Add(-time.Minute)},
		sessionBusyMu:    sync.RWMutex{},
	}
	api.terminalStore.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "main:"+sessionID, "mlp-claude-live", "claude ready\n> ", 1))

	body := bytes.NewBufferString(`{"message":"this should become a new turn"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/live-input", body)
	req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
	rr := httptest.NewRecorder()

	api.handleLiveInputMessage(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want 409 explicit delivery failure", rr.Code, rr.Body.String())
	}
	if api.isSessionBusy(sessionID) {
		t.Fatal("stale retained session should clear stale busy state even though delivery was rejected")
	}
	if got := len(store.GetAllEventsRaw(sessionID)); got != 0 {
		t.Fatalf("failed delivery recorded %d events, want 0", got)
	}
}

func TestCanSteerSessionRequiresActiveForegroundTurn(t *testing.T) {
	sessionID := "foreground-session"
	runningAgent := testCodingAgent(llm.ProviderClaudeCode, "claude-sonnet-4-6")
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		runningAgents:    map[string]*mcpagent.Agent{sessionID: runningAgent},
		runningAgentsMux: sync.RWMutex{},
		agentCancelFuncs: map[string]context.CancelFunc{},
		agentCancelMux:   sync.RWMutex{},
		// This test's subject is the turn-cancel proof; simulate the
		// registered transport the steer gate also requires.
		internalSteerTransportReady: func(provider, sid string) bool {
			return true
		},
	}

	if api.canSteerSession(sessionID) {
		t.Fatal("canSteerSession = true with only a retained agent object; want false until a foreground turn is active")
	}

	api.agentCancelMux.Lock()
	api.agentCancelFuncs[sessionID] = func() {}
	api.agentCancelMux.Unlock()
	if !api.canSteerSession(sessionID) {
		t.Fatal("canSteerSession = false with retained agent and active foreground cancel; want true")
	}
}

// A live turn whose CLI is still registering its interactive session must
// not advertise steerability: the steer would 409 delivery_uncertain.
// Found live on muse (steer at t+4s, registration later).
func TestCanSteerSessionWaitsForTransportRegistration(t *testing.T) {
	sessionID := "transport-gap-session"
	runningAgent := testCodingAgent(llm.ProviderMuseCLI, "muse-spark-1.3-contributor")
	ready := false
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		runningAgents:    map[string]*mcpagent.Agent{sessionID: runningAgent},
		runningAgentsMux: sync.RWMutex{},
		agentCancelFuncs: map[string]context.CancelFunc{sessionID: func() {}},
		agentCancelMux:   sync.RWMutex{},
		internalSteerTransportReady: func(provider, sid string) bool {
			if sid != sessionID {
				t.Errorf("hook session = %q, want %q", sid, sessionID)
			}
			return ready
		},
	}

	if api.canSteerSession(sessionID) {
		t.Fatal("canSteerSession = true before the CLI transport registers; want false")
	}
	ready = true
	if !api.canSteerSession(sessionID) {
		t.Fatal("canSteerSession = false after the CLI transport registers; want true")
	}
}

// Idle foreground-session completion keys on turn liveness, not transport
// registration: a live turn whose CLI is still registering must keep the
// session running.
func TestIdleCompletionIgnoresTransportRegistrationGap(t *testing.T) {
	sessionID := "idle-gap-session"
	runningAgent := testCodingAgent(llm.ProviderMuseCLI, "muse-spark-1.3-contributor")
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		runningAgents:    map[string]*mcpagent.Agent{sessionID: runningAgent},
		runningAgentsMux: sync.RWMutex{},
		agentCancelFuncs: map[string]context.CancelFunc{sessionID: func() {}},
		agentCancelMux:   sync.RWMutex{},
		internalSteerTransportReady: func(provider, sid string) bool {
			return false
		},
	}

	if api.shouldCompleteIdleForegroundSession(sessionID, "running", false) {
		t.Fatal("idle completion fired during a live turn with unregistered transport; want false")
	}
	if api.canSteerSession(sessionID) {
		t.Fatal("canSteerSession = true with unregistered transport; want false")
	}
}

// A retained agent object without a matching provider-native tmux registration
// is stale. Live delivery must fail explicitly and let /api/query start a clean
// resumed turn instead of silently parking the message in a steer queue.
func TestTryDeliverQueryAsLiveInputUnconfirmedSendDoesNotFallThrough(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	sessionID := "busy-coding-session"
	runningAgent := testCodingAgent(llm.ProviderClaudeCode, "claude-sonnet-4-6")
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		eventStore:       store,
		runningAgents:    map[string]*mcpagent.Agent{sessionID: runningAgent},
		runningAgentsMux: sync.RWMutex{},
		agentCancelFuncs: map[string]context.CancelFunc{sessionID: func() {}}, // active foreground turn → busy
		agentCancelMux:   sync.RWMutex{},
		// A mid-send transport failure: the CLI may or may not have seen the
		// message, so a second dispatch could double-send. Contrast with a
		// pooled-session miss, which proves non-delivery and falls through.
		internalUserMessageDeliveryHandler: func(_ context.Context, _ *mcpagent.Agent, _ mcpagent.UserMessageDeliveryRequest) (mcpagent.UserMessageDeliveryResult, error) {
			return mcpagent.UserMessageDeliveryResult{}, errors.New("send-keys to tmux pane failed mid-write")
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/query", nil)
	req.Header.Set("X-Session-ID", sessionID)
	rr := httptest.NewRecorder()

	if !api.tryDeliverQueryAsLiveInput(rr, req, sessionID, "steer me into the running turn", "query_test_busy") {
		t.Fatal("an attempted send must not fall through to a second dispatch")
	}
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "delivery_uncertain") {
		t.Fatalf("want explicit uncertain delivery, got %d %s", rr.Code, rr.Body.String())
	}
	if got := len(store.GetAllEventsRaw(sessionID)); got != 0 {
		t.Fatalf("recorded %d events, want 0 for an unconfirmed send", got)
	}
}

func TestLiveInputErrorProvesNoTarget(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"muse pool miss", errors.New("failed to submit live input to muse-cli: no active Muse interactive session registered for owner session s"), true},
		{"claude pool miss", errors.New("failed to submit live input to claude-code: no active Claude Code tmux session registered for owner session s"), true},
		{"closed turn session", errors.New("session is closed"), true},
		{"idle agent refuses its steer queue", fmt.Errorf("deliver: %w", &mcpagent.CodingAgentDeliveryError{Kind: mcpagent.DeliveryErrorKindNoSession, Provider: "claude-code", Reason: "no turn is running"}), true},
		{"other typed delivery error", &mcpagent.CodingAgentDeliveryError{Kind: mcpagent.DeliveryErrorKindTimeout}, false},
		{"mid-send failure", errors.New("send-keys to tmux pane failed mid-write"), false},
		{"timeout", errors.New("delivery_timed_out waiting for ack"), false},
	}
	for _, tc := range cases {
		if got := liveInputErrorProvesNoTarget(tc.err); got != tc.want {
			t.Errorf("%s: liveInputErrorProvesNoTarget = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A pooled-session miss proves the send never reached a CLI (the lookup runs
// before any tmux I/O), so there is no first dispatch a new turn could
// duplicate. The live-input path falls through and the message starts a
// normal turn — e.g. after a runtime rebind closed the retained CLI.
func TestTryDeliverQueryAsLiveInputPoolMissFallsThroughToNewTurn(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	sessionID := "busy-coding-session-pool-miss"
	runningAgent := testCodingAgent(llm.ProviderClaudeCode, "claude-sonnet-4-6")
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		eventStore:       store,
		runningAgents:    map[string]*mcpagent.Agent{sessionID: runningAgent},
		runningAgentsMux: sync.RWMutex{},
		agentCancelFuncs: map[string]context.CancelFunc{sessionID: func() {}},
		agentCancelMux:   sync.RWMutex{},
		// No test hook: the real delivery attempt pool-misses (no tmux
		// session is registered in a unit test) exactly like a retained CLI
		// that a restart already closed.
	}

	req := httptest.NewRequest(http.MethodPost, "/api/query", nil)
	req.Header.Set("X-Session-ID", sessionID)
	rr := httptest.NewRecorder()

	if api.tryDeliverQueryAsLiveInput(rr, req, sessionID, "steer me into the running turn", "query_test_pool_miss") {
		t.Fatalf("pool-miss delivery must fall through to a new turn, got %d %s", rr.Code, rr.Body.String())
	}
	if got := len(store.GetAllEventsRaw(sessionID)); got != 0 {
		t.Fatalf("recorded %d events, want 0 for a provably unsent message", got)
	}
}

// A running turn on a query-only transport (API continuation, no pooled TUI)
// queues the message for its next boundary instead of failing the send. The
// running-agent path accepts it with the same shape as the retained-session
// queue branch — never a 409 for a send no CLI ever saw.
func TestTryDeliverQueryAsLiveInputAcceptsQueuedDelivery(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	sessionID := "busy-api-turn-session"
	runningAgent := testCodingAgent(llm.ProviderMuseCLI, "muse-spark-1.3-contributor")
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		eventStore:       store,
		runningAgents:    map[string]*mcpagent.Agent{sessionID: runningAgent},
		runningAgentsMux: sync.RWMutex{},
		agentCancelFuncs: map[string]context.CancelFunc{sessionID: func() {}},
		agentCancelMux:   sync.RWMutex{},
		internalUserMessageDeliveryHandler: func(_ context.Context, _ *mcpagent.Agent, _ mcpagent.UserMessageDeliveryRequest) (mcpagent.UserMessageDeliveryResult, error) {
			return mcpagent.UserMessageDeliveryResult{Provider: llm.ProviderMuseCLI, DeliveryStatus: mcpagent.UserMessageDeliveryStatusQueuedForInjection}, nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/query", nil)
	req.Header.Set("X-Session-ID", sessionID)
	rr := httptest.NewRecorder()

	if !api.tryDeliverQueryAsLiveInput(rr, req, sessionID, "queue me into the API turn", "query_test_queued") {
		t.Fatal("queued delivery must be handled, not fall through to a second dispatch")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rr.Code, rr.Body.String())
	}
	var response map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["status"] != "accepted" || response["delivery_status"] != "queued_for_injection" {
		t.Fatalf("response = %v, want accepted/queued_for_injection", response)
	}
}

// Single-entry routing: a retained coding-agent CLI should accept the next
// message when there is no foreground-turn/busy proof but the live tmux pane is
// still registered. The CLI owns how to handle the input in its tmux session.
func TestTryDeliverQueryAsLiveInputRetainedCodingAgentWithStaleTmuxFallsThrough(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	sessionID := "retained-coding-session"
	runningAgent := testCodingAgent(llm.ProviderClaudeCode, "claude-sonnet-4-6")
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		eventStore:       store,
		terminalStore:    terminals.NewStore(),
		runningAgents:    map[string]*mcpagent.Agent{sessionID: runningAgent},
		runningAgentsMux: sync.RWMutex{},
		agentCancelFuncs: map[string]context.CancelFunc{}, // no active turn
		agentCancelMux:   sync.RWMutex{},
	}
	api.terminalStore.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "main:"+sessionID, "mlp-claude-retained", "claude ready\n> ", 1))

	req := httptest.NewRequest(http.MethodPost, "/api/query", nil)
	rr := httptest.NewRecorder()
	if api.tryDeliverQueryAsLiveInput(rr, req, sessionID, "next message", "query_test_retained") {
		t.Fatalf("tryDeliverQueryAsLiveInput = true for stale provider registration; want normal-turn fallback. body=%s", rr.Body.String())
	}
	if got := len(store.GetAllEventsRaw(sessionID)); got != 0 {
		t.Fatalf("failed retained CLI delivery recorded %d events, want 0", got)
	}
}

func TestTryDeliverQueryAsLiveInputReactivatesSettledRetainedTmux(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	const sessionID = "settled-retained-coding-session"
	const terminalID = sessionID + ":main:" + sessionID
	runningAgent := testCodingAgent(llm.ProviderClaudeCode, "claude-sonnet-4-6")
	terminalStore := terminals.NewStore()
	var persistedUserID, persistedSessionID, persistedMessage string
	terminalStore.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(
		time.Now(),
		sessionID,
		"main:"+sessionID,
		"mlp-claude-retained",
	))
	if _, ok := terminalStore.MarkTurnCompleted(terminalID); !ok {
		t.Fatal("expected to settle retained terminal before follow-up")
	}

	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		eventStore:       store,
		terminalStore:    terminalStore,
		activeSessions:   map[string]*ActiveSessionInfo{sessionID: {SessionID: sessionID, Status: "completed"}},
		runningAgents:    map[string]*mcpagent.Agent{sessionID: runningAgent},
		runningAgentsMux: sync.RWMutex{},
		agentCancelFuncs: map[string]context.CancelFunc{},
		agentCancelMux:   sync.RWMutex{},
		internalUserMessageDeliveryHandler: func(_ context.Context, _ *mcpagent.Agent, _ mcpagent.UserMessageDeliveryRequest) (mcpagent.UserMessageDeliveryResult, error) {
			t.Fatal("live main tmux must be delivered through the durable provider path before the Go Agent map")
			return mcpagent.UserMessageDeliveryResult{}, nil
		},
		internalRetainedTerminalInputHandler: func(_ context.Context, provider llmproviders.Provider, modelID, ownerSessionID, message string) error {
			if provider != llmproviders.ProviderClaudeCode || modelID != "" || ownerSessionID != sessionID || message != "continue the retained turn" {
				t.Fatalf("retained delivery = provider=%q model=%q session=%q message=%q", provider, modelID, ownerSessionID, message)
			}
			return nil
		},
		internalLiveInputPersistenceHandler: func(userID, ownerSessionID, message string) {
			persistedUserID, persistedSessionID, persistedMessage = userID, ownerSessionID, message
		},
	}
	store.SetEventAddedCallback(func(ownerSessionID string, event internalevents.Event) {
		terminalStore.HandleEventWithChange(ownerSessionID, event)
		api.observeRetainedMainTurnEvent(ownerSessionID, event)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/query", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "user-a"}))
	rr := httptest.NewRecorder()
	if !api.tryDeliverQueryAsLiveInput(rr, req, sessionID, "continue the retained turn", "query_retained_follow_up") {
		t.Fatalf("settled retained tmux did not accept follow-up: status=%d body=%s", rr.Code, rr.Body.String())
	}

	snapshot, ok := terminalStore.GetRaw(terminalID)
	if !ok {
		t.Fatal("reactivated terminal snapshot is missing")
	}
	if !snapshot.Active || snapshot.State != "running" || snapshot.ProcessState != "live" {
		t.Fatalf("terminal lifecycle = active %v state %q process %q, want running/live", snapshot.Active, snapshot.State, snapshot.ProcessState)
	}
	if !api.sessionHasRetainedCodingTmux(sessionID) || !api.sessionHasLiveMainCodingTmux(sessionID) {
		t.Fatal("reactivated retained main tmux must remain visible to routing and activity monitor")
	}
	if got := api.activeSessions[sessionID].Status; got != "running" {
		t.Fatalf("session status = %q, want running after confirmed follow-up delivery", got)
	}
	if persistedUserID != "user-a" || persistedSessionID != sessionID || persistedMessage != "continue the retained turn" {
		t.Fatalf("persisted live input = user=%q session=%q message=%q", persistedUserID, persistedSessionID, persistedMessage)
	}
}

func TestTryDeliverQueryAsLiveInputRetainedCodingAgentWithoutLiveTmuxFallsThrough(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	sessionID := "retained-coding-no-tmux-session"
	runningAgent := testCodingAgent(llm.ProviderClaudeCode, "claude-sonnet-4-6")
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		eventStore:       store,
		runningAgents:    map[string]*mcpagent.Agent{sessionID: runningAgent},
		runningAgentsMux: sync.RWMutex{},
		agentCancelFuncs: map[string]context.CancelFunc{}, // no active turn
		agentCancelMux:   sync.RWMutex{},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/query", nil)
	rr := httptest.NewRecorder()
	if api.tryDeliverQueryAsLiveInput(rr, req, sessionID, "next message", "query_test_retained_no_tmx") {
		t.Fatal("tryDeliverQueryAsLiveInput = true without an active foreground turn or live tmux; want normal /api/query path")
	}
	if got := len(store.GetAllEventsRaw(sessionID)); got != 0 {
		t.Fatalf("stale retained CLI fall-through recorded %d events, want 0", got)
	}
}

func TestTryDeliverQueryAsLiveInputClosesIdleDurableSessionAfterTmuxDisappears(t *testing.T) {
	const sessionID = "durable-session-with-reaped-tmux"
	retainedSession := startRegisteredTestTmuxSession(t, sessionID, "mlp-claude-code-reaped")
	api := &StreamingAPI{
		internalChatSubmissionStore: newTestChatSubmissionStore(),
		runningAgents:               map[string]*mcpagent.Agent{},
		runningAgentsMux:            sync.RWMutex{},
		agentCancelFuncs:            map[string]context.CancelFunc{},
		agentCancelMux:              sync.RWMutex{},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/query", nil)
	rr := httptest.NewRecorder()
	if api.tryDeliverQueryAsLiveInput(rr, req, sessionID, "resume after reap", "query_after_reap") {
		t.Fatalf("stale idle durable session handled request instead of falling through: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if _, ok := mcpagent.LookupSession(sessionID); ok {
		t.Fatal("stale durable session remained registered after its tmux disappeared")
	}
	if retainedSession.ActiveTurnID() != "" {
		t.Fatal("closed stale session retained an active turn")
	}
}

func TestHandleLiveInputMessageStartsNextTurnAfterTmuxReaperClosedDurableSession(t *testing.T) {
	const sessionID = "live-input-after-reaper"
	startRegisteredTestTmuxSession(t, sessionID, "mlp-claude-code-reaped-live-input")
	queryStarted := make(chan struct{}, 1)
	api := &StreamingAPI{
		internalChatSubmissionStore: newTestChatSubmissionStore(),
		runningAgents:               map[string]*mcpagent.Agent{},
		runningAgentsMux:            sync.RWMutex{},
		agentCancelFuncs:            map[string]context.CancelFunc{},
		agentCancelMux:              sync.RWMutex{},
		lastQueryRequests: map[string]QueryRequest{
			sessionID: {AgentMode: "multi-agent", Provider: string(llm.ProviderClaudeCode), ModelID: "claude-sonnet-4-6"},
		},
		internalQueryHandler: func(w http.ResponseWriter, _ *http.Request) {
			queryStarted <- struct{}{}
			_ = json.NewEncoder(w).Encode(QueryResponse{QueryID: "resumed-after-reaper"})
		},
	}

	body := bytes.NewBufferString(`{"message":"resume after tmux reaper"}`)
	req := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/live-input", body), map[string]string{"session_id": sessionID})
	rr := httptest.NewRecorder()
	api.handleLiveInputMessage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want next-turn acceptance", rr.Code, rr.Body.String())
	}
	var response LiveInputResponse
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Success || response.DeliveryStatus != "next_turn_started" {
		t.Fatalf("response = %#v, want next_turn_started", response)
	}
	select {
	case <-queryStarted:
	case <-time.After(time.Second):
		t.Fatal("resumed query did not start")
	}
	if _, ok := mcpagent.LookupSession(sessionID); ok {
		t.Fatal("stale durable session remained registered after resumed turn dispatch")
	}
}

func startRegisteredTestTmuxSession(t *testing.T, sessionID, tmuxSession string) *mcpagent.Session {
	t.Helper()
	agent := testAgentWithHandle(sessionID, llmtypes.CodingProviderSessionHandle{
		Provider:    string(llm.ProviderClaudeCode),
		Transport:   llmtypes.CodingProviderTransportTmux,
		TmuxSession: tmuxSession,
		Status:      llmtypes.CodingProviderSessionStatusIdle,
	})
	session, err := agent.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		_ = agent.Close()
	})
	return session
}

func TestTryDeliverQueryAsLiveInputNoRetainedAgentFallsThrough(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	sessionID := "missing-coding-session"
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		eventStore:       store,
		runningAgents:    map[string]*mcpagent.Agent{},
		runningAgentsMux: sync.RWMutex{},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/query", nil)
	rr := httptest.NewRecorder()
	if api.tryDeliverQueryAsLiveInput(rr, req, sessionID, "first message", "query_test_missing") {
		t.Fatal("tryDeliverQueryAsLiveInput = true without a retained agent; want normal /api/query path")
	}
	if got := len(store.GetAllEventsRaw(sessionID)); got != 0 {
		t.Fatalf("missing-agent fall-through recorded %d events, want 0", got)
	}
}

func TestHandleLiveInputMessageDeliversDirectlyToLiveMainTmuxWithoutAgent(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	const sessionID = "live-main-without-agent"
	const terminalID = sessionID + ":main:" + sessionID
	terminalStore := terminals.NewStore()
	terminalStore.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(
		time.Now(), sessionID, "main:"+sessionID, "mlp-claude-retained-direct",
	))
	if _, ok := terminalStore.MarkTurnCompleted(terminalID); !ok {
		t.Fatal("expected retained main terminal to settle before follow-up")
	}

	type liveInputCall struct {
		provider llmproviders.Provider
		modelID  string
		session  string
		message  string
	}
	calls := make(chan liveInputCall, 1)
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		eventStore:       store,
		terminalStore:    terminalStore,
		activeSessions:   map[string]*ActiveSessionInfo{sessionID: {SessionID: sessionID, Status: "completed"}},
		runningAgents:    map[string]*mcpagent.Agent{},
		runningAgentsMux: sync.RWMutex{},
		internalRetainedTerminalInputHandler: func(_ context.Context, provider llmproviders.Provider, modelID, ownerSessionID, message string) error {
			calls <- liveInputCall{provider: provider, modelID: modelID, session: ownerSessionID, message: message}
			return nil
		},
	}
	store.SetEventAddedCallback(func(ownerSessionID string, event internalevents.Event) {
		terminalStore.HandleEventWithChange(ownerSessionID, event)
		api.observeRetainedMainTurnEvent(ownerSessionID, event)
	})

	body := bytes.NewBufferString(`{"message":"deliver directly to the retained Claude pane"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/live-input", body)
	req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
	rr := httptest.NewRecorder()

	api.handleLiveInputMessage(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var response LiveInputResponse
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Success || response.DeliveryStatus != "sent_to_cli" || response.Provider != string(llm.ProviderClaudeCode) {
		t.Fatalf("response = %#v, want confirmed Claude CLI delivery", response)
	}

	select {
	case got := <-calls:
		if got.provider != llmproviders.ProviderClaudeCode || got.modelID != "" || got.session != sessionID || got.message != "deliver directly to the retained Claude pane" {
			t.Fatalf("retained terminal delivery = %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("retained terminal live-input sender was not called")
	}

	snapshot, ok := terminalStore.GetRaw(terminalID)
	if !ok || !snapshot.Active || snapshot.ProcessState != "live" || snapshot.State != "running" {
		t.Fatalf("terminal lifecycle after retained delivery = %#v, want active running/live", snapshot)
	}
	if got := api.activeSessions[sessionID].Status; got != "running" {
		t.Fatalf("session status = %q, want running", got)
	}
	if !api.isSessionBusy(sessionID) {
		t.Fatal("confirmed retained-terminal turn must be authoritatively busy while it is running")
	}
	if runtime, _ := api.authoritativeRuntimeSnapshot(sessionID); runtime.Phase != runtimePhaseRunning {
		t.Fatalf("runtime phase = %q (%s), want running", runtime.Phase, runtime.Reason)
	}
	// Count only the user_message row: the sent_to_cli delivery also
	// starts the durable watch, whose live_input_confirmed event may
	// race this assertion (watch routing is covered in
	// live_input_durable_test.go).
	if got := filterEventsByType(store.GetAllEventsRaw(sessionID), string(pkgevents.UserMessage)); len(got) != 1 {
		t.Fatalf("recorded user_message count = %d, want 1", len(got))
	}

	// A child terminal completion is part of the formatted transcript but must
	// never settle the retained main-agent turn.
	childExecutionID := "workflow-step:child-review"
	store.AddEvent(sessionID, codingAgentTmuxReaperChunkEvent(
		time.Now(), sessionID, childExecutionID, "mlp-claude-child",
	))
	store.AddEvent(sessionID, internalevents.Event{
		Type:          "streaming_end",
		Timestamp:     time.Now(),
		SessionID:     sessionID,
		ExecutionID:   childExecutionID,
		ExecutionKind: "workflow_step",
		Data: &pkgevents.AgentEvent{
			Type: pkgevents.StreamingEnd,
			Data: &pkgevents.StreamingEndEvent{BaseEventData: pkgevents.BaseEventData{Metadata: map[string]interface{}{
				"kind": "terminal", "tmux_session": "mlp-claude-child",
				"execution_kind": "workflow_step", "scope": "workflow_step",
			}}},
		},
	})
	if !api.isSessionBusy(sessionID) {
		t.Fatal("child completion incorrectly settled the retained main-agent turn")
	}

	// The main terminal's structured end event is the same boundary consumed by
	// the Formatted view. It settles the logical turn but retains the live tmux.
	store.AddEvent(sessionID, internalevents.Event{
		Type:          "streaming_end",
		Timestamp:     time.Now(),
		SessionID:     sessionID,
		ExecutionID:   "main:" + sessionID,
		ExecutionKind: "main_agent",
		Data: &pkgevents.AgentEvent{
			Type: pkgevents.StreamingEnd,
			Data: &pkgevents.StreamingEndEvent{BaseEventData: pkgevents.BaseEventData{Metadata: map[string]interface{}{
				"kind": "terminal", "tmux_session": "mlp-claude-retained-direct",
				"execution_kind": "main_agent", "scope": "main_agent",
			}}},
		},
	})
	if api.isSessionBusy(sessionID) {
		t.Fatal("structured main-agent completion did not clear retained-turn busy state")
	}
	if got := api.activeSessions[sessionID].Status; got != "completed" {
		t.Fatalf("session status after structured completion = %q, want completed", got)
	}
	if runtime, _ := api.authoritativeRuntimeSnapshot(sessionID); runtime.Phase != runtimePhaseCompleted {
		t.Fatalf("runtime phase after structured completion = %q (%s), want completed", runtime.Phase, runtime.Reason)
	}
	settled, ok := terminalStore.GetRaw(terminalID)
	if !ok || settled.Active || settled.ProcessState != "live" {
		t.Fatalf("retained terminal after structured completion = %#v, want inactive with live process", settled)
	}
}

func TestRetainedTerminalDeliveryUsesLiveProviderAfterAutomationSwitch(t *testing.T) {
	const sessionID = "workflow-provider-switched"
	terminalStore := terminals.NewStore()
	terminalStore.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(
		time.Now(), sessionID, "main:"+sessionID, "mlp-codex-cli-int-switched",
	))

	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		terminalStore: terminalStore,
		lastQueryRequests: map[string]QueryRequest{
			sessionID: {
				Provider: string(llm.ProviderClaudeCode),
				ModelID:  "claude-opus-5",
				LLMConfig: &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{
					Provider: string(llm.ProviderClaudeCode),
					ModelID:  "claude-opus-5",
				}},
			},
		},
		internalRetainedTerminalInputHandler: func(_ context.Context, provider llmproviders.Provider, modelID, ownerSessionID, message string) error {
			if provider != llmproviders.ProviderCodexCLI {
				t.Fatalf("provider = %q, want live Codex tmux provider", provider)
			}
			if modelID != "" {
				t.Fatalf("model = %q, want stale Claude model discarded", modelID)
			}
			if ownerSessionID != sessionID || message != "continue with Codex" {
				t.Fatalf("delivery session=%q message=%q", ownerSessionID, message)
			}
			return nil
		},
	}

	provider, handled, err := api.deliverRetainedMainTerminalInput(context.Background(), sessionID, "continue with Codex")
	if err != nil || !handled || provider != string(llm.ProviderCodexCLI) {
		t.Fatalf("delivery provider=%q handled=%v err=%v", provider, handled, err)
	}
}

func TestRetainedTurnCompletionUsesSidecarFinalResponse(t *testing.T) {
	const sessionID = "retained-sidecar-completion"
	const finalResponse = "This is the final assistant response from the sidecar."
	startedAt := time.Now().Add(-2 * time.Second)
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		eventStore: store,
		retainedMainTurns: map[string]time.Time{
			sessionID: startedAt,
		},
		retainedMainTurnExecutionIDs: map[string]string{
			sessionID: "live-turn:test",
		},
		internalRetainedTurnFinalResponseReader: func(provider llmproviders.Provider, ownerSessionID string, turnStart time.Time) string {
			if provider != llmproviders.ProviderCodexCLI || ownerSessionID != sessionID {
				t.Fatalf("sidecar lookup provider=%q owner=%q", provider, ownerSessionID)
			}
			if !turnStart.Equal(startedAt) {
				t.Fatalf("sidecar lookup turn start=%v, want %v", turnStart, startedAt)
			}
			return finalResponse
		},
	}

	api.emitRetainedMainTurnStreamCompletion(sessionID, terminals.Snapshot{
		TerminalID:  sessionID + ":main:" + sessionID,
		TmuxSession: "mlp-codex-cli-int-retained",
	}, llmproviders.ProviderCodexCLI, "completed", "")

	recorded := store.GetAllEventsRaw(sessionID)
	if len(recorded) != 1 {
		t.Fatalf("recorded events=%d, want 1", len(recorded))
	}
	agentEvent := recorded[0].Data
	if agentEvent == nil {
		t.Fatal("event data is nil, want AgentEvent")
	}
	completion, ok := agentEvent.Data.(*pkgevents.UnifiedCompletionEvent)
	if !ok {
		t.Fatalf("agent event data type=%T, want *events.UnifiedCompletionEvent", agentEvent.Data)
	}
	if completion.FinalResult != finalResponse || completion.Status != "completed" {
		t.Fatalf("completion=%#v", completion)
	}
	if completion.Metadata["source"] != "coding_agent_sidecar" {
		t.Fatalf("completion source=%v", completion.Metadata["source"])
	}
}

// Idle-composer detection and a closed control stream that already sees a
// durable final response can both independently decide the same retained
// turn just finished, and both call emitRetainedMainTurnStreamCompletion for
// it -- reported live as the identical "Agent · Response" answer rendered
// twice in a row. The second call for the same turn must be a no-op.
func TestRetainedTurnCompletionSkipsDuplicateForSameTurn(t *testing.T) {
	const sessionID = "retained-duplicate-completion"
	const finalResponse = "The full route run completed successfully end-to-end."
	startedAt := time.Now().Add(-2 * time.Second)
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		eventStore: store,
		retainedMainTurns: map[string]time.Time{
			sessionID: startedAt,
		},
		retainedMainTurnExecutionIDs: map[string]string{
			sessionID: "live-turn:test",
		},
		internalRetainedTurnFinalResponseReader: func(llmproviders.Provider, string, time.Time) string {
			return finalResponse
		},
	}

	snapshot := terminals.Snapshot{TerminalID: sessionID + ":main:" + sessionID, TmuxSession: "mlp-codex-cli-int-retained"}
	// The idle-composer path fires first...
	api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, llmproviders.ProviderCodexCLI, "completed", "")
	// ...then the control-stream-closed path independently reaches the same
	// conclusion for the same turn a few seconds later.
	api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, llmproviders.ProviderCodexCLI, "completed", "")

	recorded := store.GetAllEventsRaw(sessionID)
	if len(recorded) != 1 {
		t.Fatalf("recorded events=%d, want 1 (duplicate completion for the same turn must be suppressed)", len(recorded))
	}
}

// A genuinely new turn (retainedMainTurns advances to a new start time) must
// still get its own completion -- the guard is per-turn, not per-session.
func TestRetainedTurnCompletionEmitsAgainForANewTurn(t *testing.T) {
	const sessionID = "retained-new-turn-completion"
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		eventStore: store,
		retainedMainTurns: map[string]time.Time{
			sessionID: time.Now().Add(-2 * time.Second),
		},
		retainedMainTurnExecutionIDs: map[string]string{
			sessionID: "live-turn:test",
		},
		internalRetainedTurnFinalResponseReader: func(llmproviders.Provider, string, time.Time) string {
			return "first turn's answer"
		},
	}
	snapshot := terminals.Snapshot{TerminalID: sessionID + ":main:" + sessionID, TmuxSession: "mlp-codex-cli-int-retained"}
	api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, llmproviders.ProviderCodexCLI, "completed", "")

	// A new turn starts, advancing the tracked start time.
	api.retainedMainTurns[sessionID] = time.Now()
	api.internalRetainedTurnFinalResponseReader = func(llmproviders.Provider, string, time.Time) string {
		return "second turn's answer"
	}
	api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, llmproviders.ProviderCodexCLI, "completed", "")

	recorded := store.GetAllEventsRaw(sessionID)
	if len(recorded) != 2 {
		t.Fatalf("recorded events=%d, want 2 (a new turn must still get its own completion)", len(recorded))
	}
}

// API/LLM unchanged: even when busy, a non-coding (API) agent must NOT be
// short-circuited — those keep their frontend steer-vs-queue path.
func TestTryDeliverQueryAsLiveInputSkipsNonCodingAgent(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	sessionID := "busy-llm-session"
	runningAgent := testCodingAgent(llm.ProviderOpenAI, "gpt-5")
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		eventStore:       store,
		runningAgents:    map[string]*mcpagent.Agent{sessionID: runningAgent},
		runningAgentsMux: sync.RWMutex{},
		agentCancelFuncs: map[string]context.CancelFunc{sessionID: func() {}}, // busy
		agentCancelMux:   sync.RWMutex{},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/query", nil)
	rr := httptest.NewRecorder()
	if api.tryDeliverQueryAsLiveInput(rr, req, sessionID, "llm message", "query_test_llm") {
		t.Fatal("tryDeliverQueryAsLiveInput = true for an API/LLM agent; want false (API/LLM routing unchanged)")
	}
}

func TestRequestLLMConfigOverridesManifestOnlyForAgentProfileSource(t *testing.T) {
	req := QueryRequest{
		LLMConfig: &orchestrator.LLMConfig{
			Primary: orchestrator.LLMModel{Provider: "claude-code", ModelID: "claude-sonnet-5"},
		},
	}
	if requestLLMConfigOverridesManifest(req) {
		t.Fatal("untagged request LLM config should not override workflow manifest phase LLM")
	}

	req.LLMConfigSource = llmConfigSourceAgentProfile
	if !requestLLMConfigOverridesManifest(req) {
		t.Fatal("agent-profile LLM config should override workflow manifest phase LLM")
	}

	// A scheduler Pulse turn no longer overrides the manifest: the whole
	// scheduled conversation stays on the Builder model, and pulse_llm is
	// applied by the workshop to the background review agents instead.
	req.LLMConfigSource = "scheduled_pulse"
	if requestLLMConfigOverridesManifest(req) {
		t.Fatal("the retired scheduled_pulse source must not override the workflow manifest")
	}

	req.LLMConfigSource = "manual"
	if requestLLMConfigOverridesManifest(req) {
		t.Fatal("manual request LLM config should keep workflow manifest as source of truth")
	}
}

func TestSessionInputLaneSerializesRapidInteractiveSubmits(t *testing.T) {
	sessionID := "rapid-submit-session"
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		sessionInputLanes: make(map[string]*sessionInputLane),
	}

	releaseFirst := api.lockSessionInputLane(sessionID)
	attemptingSecond := make(chan struct{})
	acquiredSecond := make(chan struct{})
	releasedSecond := make(chan struct{})

	go func() {
		close(attemptingSecond)
		releaseSecond := api.lockSessionInputLane(sessionID)
		close(acquiredSecond)
		releaseSecond()
		close(releasedSecond)
	}()

	<-attemptingSecond
	select {
	case <-acquiredSecond:
		t.Fatal("second submit acquired the same session input lane before the first released it")
	case <-time.After(75 * time.Millisecond):
	}

	releaseFirst()
	select {
	case <-acquiredSecond:
	case <-time.After(time.Second):
		t.Fatal("second submit did not acquire the input lane after the first released it")
	}
	<-releasedSecond
	api.sessionInputLanesMu.Lock()
	remainingLanes := len(api.sessionInputLanes)
	api.sessionInputLanesMu.Unlock()
	if remainingLanes != 0 {
		t.Fatalf("session input lane registry retained %d idle lane(s), want 0", remainingLanes)
	}
}

func TestLiveInputDoesNotWaitForQueryLaunchLane(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	const sessionID = "live-input-during-launch"
	runningAgent := testCodingAgent(llm.ProviderClaudeCode, "claude-sonnet-4-6")
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		eventStore:        store,
		terminalStore:     terminals.NewStore(),
		runningAgents:     map[string]*mcpagent.Agent{sessionID: runningAgent},
		runningAgentsMux:  sync.RWMutex{},
		agentCancelFuncs:  map[string]context.CancelFunc{sessionID: func() {}},
		agentCancelMux:    sync.RWMutex{},
		sessionInputLanes: make(map[string]*sessionInputLane),
	}
	api.terminalStore.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "main:"+sessionID, "missing-test-tmux", "claude ready\n> ", 1))

	releaseLaunchLane := api.lockSessionInputLane(sessionID)
	defer releaseLaunchLane()

	body := bytes.NewBufferString(`{"message":"must not wait behind launch"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/live-input", body)
	req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		api.handleLiveInputMessage(rr, req)
		close(done)
	}()

	select {
	case <-done:
		if rr.Code != http.StatusConflict {
			t.Fatalf("status = %d body=%s, want explicit delivery failure without blocking", rr.Code, rr.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("live input blocked behind the long-running query launch lane")
	}
}

func TestStartNextTurnFromLiveInputAcknowledgesBeforeQueuedTurnRuns(t *testing.T) {
	const sessionID = "queued-next-turn"
	handlerStarted := make(chan *http.Request, 1)
	releaseHandler := make(chan struct{})
	handlerDone := make(chan struct{})

	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		lastQueryRequests: map[string]QueryRequest{
			sessionID: {
				AgentMode: "multi-agent",
				Provider:  string(llm.ProviderCodexCLI),
				ModelID:   "gpt-5.6-sol",
			},
		},
		internalQueryHandler: func(w http.ResponseWriter, req *http.Request) {
			handlerStarted <- req
			<-releaseHandler
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(QueryResponse{QueryID: "queued-query"})
			close(handlerDone)
		},
	}

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/live-input", nil).WithContext(requestCtx)
	req.Header.Set("X-Session-ID", sessionID)
	rr := httptest.NewRecorder()

	returned := make(chan bool, 1)
	go func() {
		returned <- api.startNextTurnFromLiveInput(rr, req, sessionID, "send after this turn", nil)
	}()

	select {
	case handled := <-returned:
		if !handled {
			t.Fatal("startNextTurnFromLiveInput returned false")
		}
	case <-time.After(time.Second):
		t.Fatal("live-input acknowledgement waited for the queued next turn")
	}

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var response LiveInputResponse
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.DeliveryStatus != "next_turn_started" || response.MessageID == "" {
		t.Fatalf("response = %#v, want accepted next turn with message ID", response)
	}

	var queuedReq *http.Request
	select {
	case queuedReq = <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("queued next-turn handler did not start")
	}
	cancelRequest()
	if err := queuedReq.Context().Err(); err != nil {
		t.Fatalf("queued next-turn context inherited live-input cancellation: %v", err)
	}
	close(releaseHandler)
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("queued next-turn handler did not finish")
	}
}

func TestStartNextTurnFromLiveInputReturnsConflictWhenBuilderChatActuallyBusy(t *testing.T) {
	const sessionID = "queued-next-turn"
	now := time.Now().UTC()
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		lastQueryRequests: map[string]QueryRequest{
			sessionID: {
				AgentMode:      "workflow_phase",
				PhaseID:        "workflow-builder",
				SelectedFolder: "Workflow/social-media",
				Provider:       string(llm.ProviderClaudeCode),
				ModelID:        "claude-opus-4-8",
			},
		},
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			"builder-chat": {
				ExecutionID:   "builder-chat",
				SessionID:     "different-builder-session",
				Source:        trackedExecutionSourceWorkshopBackground,
				Kind:          "workflow_builder_task",
				WorkspacePath: "Workflow/social-media",
				PhaseID:       "workflow-builder",
				Status:        trackedExecutionStatusRunning,
				TriggeredBy:   "workflow_builder",
				StartedAt:     now,
			},
		},
		internalQueryHandler: func(w http.ResponseWriter, req *http.Request) {
			t.Fatal("queued next-turn handler must not start when builder chat is busy")
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/live-input", nil)
	req.Header.Set("X-Session-ID", sessionID)
	rr := httptest.NewRecorder()

	if !api.startNextTurnFromLiveInput(rr, req, sessionID, "send after this turn", nil) {
		t.Fatal("startNextTurnFromLiveInput returned false")
	}
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want 409", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "workflow_busy") {
		t.Fatalf("body = %s, want workflow_busy", rr.Body.String())
	}
}

func TestStartNextTurnFromLiveInputDoesNotBlockScheduledMessageSequence(t *testing.T) {
	const sessionID = "interactive-builder-session"
	now := time.Now().UTC()
	handled := make(chan QueryRequest, 1)
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		lastQueryRequests: map[string]QueryRequest{
			sessionID: {
				AgentMode:      "workflow_phase",
				PhaseID:        "workflow-builder",
				SelectedFolder: "Workflow/social-media",
				Provider:       string(llm.ProviderClaudeCode),
				ModelID:        "claude-opus-4-8",
			},
		},
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			"scheduled-message-sequence": {
				ExecutionID:   "msgseq-execute-allocate-execute-and-verify-1",
				SessionID:     "schedule-cron--social-media_1",
				Source:        trackedExecutionSourceWorkshopBackground,
				Kind:          "workflow_builder_task",
				WorkspacePath: "Workflow/social-media",
				PhaseID:       "workflow-builder",
				Status:        trackedExecutionStatusRunning,
				TriggeredBy:   "workflow_builder",
				StartedAt:     now,
			},
		},
		internalQueryHandler: func(w http.ResponseWriter, req *http.Request) {
			var got QueryRequest
			if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
				t.Errorf("decode queued request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			handled <- got
			w.WriteHeader(http.StatusOK)
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/live-input", nil)
	req.Header.Set("X-Session-ID", sessionID)
	rr := httptest.NewRecorder()

	if !api.startNextTurnFromLiveInput(rr, req, sessionID, "tell me the strategies", nil) {
		t.Fatal("startNextTurnFromLiveInput returned false")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rr.Code, rr.Body.String())
	}

	select {
	case got := <-handled:
		if got.Query != "tell me the strategies" || got.Message != "tell me the strategies" {
			t.Fatalf("queued query = %#v, want the live-input message", got)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduled message-sequence prevented the interactive continuation from starting")
	}
}

func TestQueryRequestForContinuationRestoresWorkflowPhaseContext(t *testing.T) {
	req := QueryRequest{
		AgentMode:     "multi-agent", // adapted engine mode inside handleQuery
		PhaseID:       "workflow-builder",
		PresetQueryID: "wf-marketing",
		Query:         "continue the workflow",
	}

	got := queryRequestForContinuation(req, true, "Workflow/llmprovideropensourcemarketing")
	if got.AgentMode != "workflow_phase" {
		t.Fatalf("agent mode = %q, want workflow_phase", got.AgentMode)
	}
	if got.PhaseID != req.PhaseID || got.PresetQueryID != req.PresetQueryID {
		t.Fatalf("workflow identity changed: phase=%q preset=%q", got.PhaseID, got.PresetQueryID)
	}
	if got.SelectedFolder != "Workflow/llmprovideropensourcemarketing" {
		t.Fatalf("selected folder = %q, want workflow folder", got.SelectedFolder)
	}
}

func TestQueryRequestForContinuationLeavesMultiAgentContextUnchanged(t *testing.T) {
	req := QueryRequest{AgentMode: "multi-agent", Query: "continue chat", SelectedFolder: "custom"}
	got := queryRequestForContinuation(req, false, "Workflow/ignored")
	if got.AgentMode != req.AgentMode || got.SelectedFolder != req.SelectedFolder {
		t.Fatalf("non-workflow continuation changed: got=%#v want=%#v", got, req)
	}
}

func TestQueryRequestWithEffectiveRuntimeReplacesStaleProviderWithoutMutatingSource(t *testing.T) {
	sourceConfig := &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{
		Provider: string(llm.ProviderClaudeCode),
		ModelID:  "claude-opus-5",
	}}
	req := QueryRequest{
		Provider:  string(llm.ProviderClaudeCode),
		ModelID:   "claude-opus-5",
		LLMConfig: sourceConfig,
	}

	got := queryRequestWithEffectiveRuntime(req, string(llm.ProviderCodexCLI), "gpt-5.6-sol")
	if got.Provider != string(llm.ProviderCodexCLI) || got.ModelID != "gpt-5.6-sol" {
		t.Fatalf("effective runtime = %s/%s", got.Provider, got.ModelID)
	}
	if got.LLMConfig == nil || got.LLMConfig.Primary.Provider != string(llm.ProviderCodexCLI) || got.LLMConfig.Primary.ModelID != "gpt-5.6-sol" {
		t.Fatalf("stored LLM config = %#v", got.LLMConfig)
	}
	if sourceConfig.Primary.Provider != string(llm.ProviderClaudeCode) || sourceConfig.Primary.ModelID != "claude-opus-5" {
		t.Fatalf("source request config was mutated: %#v", sourceConfig.Primary)
	}
}

func TestAgentWorksChatQueriesUseInteractiveInputLane(t *testing.T) {
	if !shouldSerializeInteractiveQueryInput(QueryRequest{AgentMode: "multi-agent"}) {
		t.Fatal("AgentWorks multi-agent query must use the session input lane")
	}
	if !shouldSerializeInteractiveQueryInput(QueryRequest{AgentMode: "simple"}) {
		t.Fatal("simple chat query must use the session input lane")
	}
	if !shouldSerializeInteractiveQueryInput(QueryRequest{AgentMode: "multi-agent", IsAutoNotification: true}) {
		t.Fatal("auto-notification turns must share the same full-turn lane")
	}
}

func TestIdleCompletionDoesNotCompleteStaleBusyTurn(t *testing.T) {
	sessionID := "stale-busy-session"
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(),
		sessionBusy:      map[string]bool{sessionID: true},
		sessionBusySince: map[string]time.Time{sessionID: time.Now().Add(-autoNotificationStaleBusyAfter - time.Second)},
		sessionBusyMu:    sync.RWMutex{},
		agentCancelFuncs: map[string]context.CancelFunc{},
		agentCancelMux:   sync.RWMutex{},
		runningAgents:    map[string]*mcpagent.Agent{},
		runningAgentsMux: sync.RWMutex{},
	}

	if api.shouldCompleteIdleForegroundSession(sessionID, "running", false) {
		t.Fatal("stale busy turn should not be completed by passive idle polling")
	}
	if !api.isSessionBusy(sessionID) {
		t.Fatal("idle-completion check must not clear the busy flag")
	}
}

func TestDelegationStartEventParentsToBackgroundAgent(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	sessionID := "session-background-owner"
	backgroundAgentID := "bg-agent-123"
	delegationID := "delegation-child-456"
	api := &StreamingAPI{internalChatSubmissionStore: newTestChatSubmissionStore(), eventStore: store}

	api.emitDelegationStartEvent(sessionID, delegationID, 1, "inspect logs", "high", "claude-sonnet-4-6", []string{"api-bridge"}, backgroundAgentID, "worker")

	rawEvents := store.GetAllEventsRaw(sessionID)
	if len(rawEvents) != 1 {
		t.Fatalf("raw event count = %d, want 1", len(rawEvents))
	}
	event := rawEvents[0]
	if event.Type != "delegation_start" {
		t.Fatalf("event type = %q, want delegation_start", event.Type)
	}
	if event.Data == nil {
		t.Fatal("event data is nil")
	}
	wantParentID := sessionID + "_background_agent_started_" + backgroundAgentID
	if event.Data.ParentID != wantParentID {
		t.Fatalf("parent_id = %q, want %q", event.Data.ParentID, wantParentID)
	}
	if event.Data.CorrelationID != delegationID {
		t.Fatalf("correlation_id = %q, want %q", event.Data.CorrelationID, delegationID)
	}
}

func filterEventsByType(events []internalevents.Event, eventType string) []internalevents.Event {
	var out []internalevents.Event
	for _, event := range events {
		if event.Type == eventType {
			out = append(out, event)
		}
	}
	return out
}

func assertLiveCodingUserMessageEvent(t *testing.T, event internalevents.Event, sessionID, provider string) {
	t.Helper()
	if event.Type != string(pkgevents.UserMessage) {
		t.Fatalf("event type = %q, want user_message", event.Type)
	}
	if event.SessionID != sessionID {
		t.Fatalf("event session = %q, want %q", event.SessionID, sessionID)
	}
	if event.Data == nil {
		t.Fatal("event data is nil")
	}
	if event.Data.Component != "coding_agent_live_input" {
		t.Fatalf("component = %q, want coding_agent_live_input", event.Data.Component)
	}
	msg, ok := event.Data.Data.(*pkgevents.UserMessageEvent)
	if !ok {
		t.Fatalf("event payload type = %T, want *UserMessageEvent", event.Data.Data)
	}
	if msg.Content != "show exact sequence item" {
		t.Fatalf("content = %q, want live message", msg.Content)
	}
	if msg.Role != "user" {
		t.Fatalf("role = %q, want user", msg.Role)
	}
	if msg.Metadata["source"] != "coding_agent_live_input" {
		t.Fatalf("metadata source = %#v, want coding_agent_live_input", msg.Metadata["source"])
	}
	if msg.Metadata["provider"] != provider {
		t.Fatalf("metadata provider = %#v, want %q", msg.Metadata["provider"], provider)
	}
	if msg.Metadata["message_id"] != "test-message-id" {
		t.Fatalf("metadata message_id = %#v, want test-message-id", msg.Metadata["message_id"])
	}
	if msg.Metadata["delivery_status"] != "sent_to_cli" {
		t.Fatalf("metadata delivery_status = %#v, want sent_to_cli", msg.Metadata["delivery_status"])
	}
}
