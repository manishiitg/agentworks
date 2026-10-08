package server

import (
	"context"
	"sync"
	"testing"
	"time"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestSteeredCompletionKeepsQueryTreeOpenForRetry(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	const sessionID = "scheduled-retry-session"
	const rootID = "scheduled-message-root"
	runningAgent := testCodingAgent(llm.ProviderCodexCLI, "codex-cli")
	api := lifecycleTestAPI()
	api.eventStore = store
	api.runningAgents = map[string]*mcpagent.Agent{sessionID: runningAgent}
	api.agentCancelFuncs = map[string]context.CancelFunc{sessionID: func() {}}
	api.internalUserMessageDeliveryHandler = func(_ context.Context, _ *mcpagent.Agent, _ mcpagent.UserMessageDeliveryRequest) (mcpagent.UserMessageDeliveryResult, error) {
		return mcpagent.UserMessageDeliveryResult{
			Provider:       llm.ProviderCodexCLI,
			DeliveryStatus: mcpagent.UserMessageDeliveryStatusSentToCLI,
			Transport:      llm.CodingAgentTransportTmux,
		}, nil
	}

	now := time.Now().UTC()
	rootDone := now.Add(time.Second)
	api.trackedWorkflowExecutions[rootID] = &TrackedWorkflowExecution{
		ExecutionID: rootID, SessionID: sessionID, Source: trackedExecutionSourceConversationTurn,
		Status: trackedExecutionStatusCompleted, StartedAt: now, CompletedAt: &rootDone,
	}
	childDone := now.Add(2 * time.Second)
	bg := &BackgroundAgent{
		ID: "workflow-attempt-1", ParentExecutionID: rootID, Name: "full workflow",
		SessionID: sessionID, Status: BGAgentFailed, Error: "first attempt failed",
		CreatedAt: now, CompletedAt: &childDone,
	}
	api.bgAgentRegistry.Register(sessionID, bg)

	if !api.steerBackgroundAgentCompletion(sessionID, bg.ID) {
		t.Fatal("steered completion was not delivered")
	}

	continuationID := api.currentConversationTurnExecutionID(sessionID)
	if continuationID == "" || continuationID == rootID {
		t.Fatalf("continuation id = %q, want a new running synthetic turn", continuationID)
	}
	if got := trackedExecutionParentID(api.trackedWorkflowExecutions[continuationID]); got != rootID {
		t.Fatalf("continuation parent = %q, want %q", got, rootID)
	}

	api.trackedWorkflowExecutions["workflow-retry"] = &TrackedWorkflowExecution{
		ExecutionID: "workflow-retry", SessionID: sessionID, Status: trackedExecutionStatusRunning,
		StartedAt: time.Now().UTC(), Metadata: map[string]string{"parent_execution_id": rootID},
	}
	if state := api.conversationTurnTreeSnapshot(rootID); state.terminal() || state.RunningChildren != 2 {
		t.Fatalf("root advanced while continuation and retry were running: %+v", state)
	}

	api.completeTrackedExecution(continuationID, trackedExecutionStatusCompleted, "", nil)
	if state := api.conversationTurnTreeSnapshot(rootID); state.terminal() || state.RunningChildren != 1 {
		t.Fatalf("root advanced before retry completed: %+v", state)
	}
	api.completeTrackedExecution("workflow-retry", trackedExecutionStatusCompleted, "", nil)
	if state := api.conversationTurnTreeSnapshot(rootID); !state.terminal() {
		t.Fatalf("root did not finish after retry completed: %+v", state)
	}
}

// TestSteerBackgroundAgentCompletionFallsBackForFailedLiveDelivery verifies
// that an unconfirmed tmux send is not hidden in the agent steer queue. The
// caller-owned notification queue remains responsible for retrying it.
func TestSteerBackgroundAgentCompletionFallsBackForFailedLiveDelivery(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	sessionID := "busy-steerable-session"
	runningAgent := testCodingAgent(llm.ProviderCodexCLI, "codex-cli")

	api := &StreamingAPI{
		eventStore:       store,
		runningAgents:    map[string]*mcpagent.Agent{sessionID: runningAgent},
		runningAgentsMux: sync.RWMutex{},
		// An active foreground turn cancel handle plus a registered CLI
		// transport is the proof that makes the session steerable
		// (canSteerSession). The hook simulates the registered transport;
		// this test's subject is the delivery fallback below it.
		agentCancelFuncs: map[string]context.CancelFunc{sessionID: func() {}},
		agentCancelMux:   sync.RWMutex{},
		bgAgentRegistry:  NewBackgroundAgentRegistry(),
		internalSteerTransportReady: func(provider, sid string) bool {
			return true
		},
	}

	bg := &BackgroundAgent{
		ID:        "workflow-full-1",
		Name:      "full-workflow [default / iteration-0]",
		SessionID: sessionID,
		Status:    BGAgentFailed,
		Error:     "step 12 execution failed: input file not found",
	}
	api.bgAgentRegistry.Register(sessionID, bg)

	if !api.canSteerSession(sessionID) {
		t.Fatal("precondition: expected session to be steerable")
	}

	if api.steerBackgroundAgentCompletion(sessionID, bg.ID) {
		t.Fatal("steerBackgroundAgentCompletion = true for queued injection; want false so caller queues")
	}

	// Failed live delivery is not definitive, so the completion must not be
	// flagged notified. The caller's queue/drain backstop will redeliver it later.
	bg.mu.RLock()
	notified := bg.completionNotification == completionNotificationDelivered
	bg.mu.RUnlock()
	if notified {
		t.Fatal("agent.notified = true after queued injection; want false so the queue path can retry")
	}

	if api.steerBackgroundAgentCompletion(sessionID, bg.ID) {
		t.Fatal("second queued-injection steer call = true; want false until delivery is confirmed")
	}
}

// TestSteerBackgroundAgentCompletionFallsBackWhenNoRunningAgent verifies that the
// steer path declines (returns false) when there is no running agent to receive
// the message, so the caller falls back to the queue + drain-on-idle backstop.
func TestSteerBackgroundAgentCompletionFallsBackWhenNoRunningAgent(t *testing.T) {
	api := &StreamingAPI{
		runningAgents:    map[string]*mcpagent.Agent{},
		runningAgentsMux: sync.RWMutex{},
		bgAgentRegistry:  NewBackgroundAgentRegistry(),
	}
	const sessionID = "no-running-agent"
	bg := &BackgroundAgent{ID: "bg-1", SessionID: sessionID, Status: BGAgentCompleted, Result: "done"}
	api.bgAgentRegistry.Register(sessionID, bg)

	if api.steerBackgroundAgentCompletion(sessionID, bg.ID) {
		t.Fatal("steerBackgroundAgentCompletion = true with no running agent; want false so caller queues")
	}
	bg.mu.RLock()
	notified := bg.completionNotification == completionNotificationDelivered
	bg.mu.RUnlock()
	if notified {
		t.Fatal("agent.notified = true after a declined steer; the queue path must still own delivery")
	}
}

func TestSteerBackgroundAgentCompletionDefersPlainDelegation(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	sessionID := "busy-chief-session"
	runningAgent := testCodingAgent(llm.ProviderClaudeCode, "claude-code")

	api := &StreamingAPI{
		eventStore:       store,
		runningAgents:    map[string]*mcpagent.Agent{sessionID: runningAgent},
		runningAgentsMux: sync.RWMutex{},
		agentCancelFuncs: map[string]context.CancelFunc{sessionID: func() {}},
		agentCancelMux:   sync.RWMutex{},
		bgAgentRegistry:  NewBackgroundAgentRegistry(),
	}

	bg := &BackgroundAgent{
		ID:        "delegate-1",
		Name:      "Research task",
		SessionID: sessionID,
		Kind:      "delegation",
		Status:    BGAgentCompleted,
		Result:    "done",
	}
	api.bgAgentRegistry.Register(sessionID, bg)

	if api.steerBackgroundAgentCompletion(sessionID, bg.ID) {
		t.Fatal("plain delegation completion should not be live-steered; it needs a separate synthetic completion turn")
	}

	bg.mu.RLock()
	notified := bg.completionNotification == completionNotificationDelivered
	bg.mu.RUnlock()
	if notified {
		t.Fatal("plain delegation completion was marked notified before the synthetic turn")
	}
}

// Regression: four sibling chats answered a Code chat's asks; the last
// answer was steered into the turn that was answering the previous one. That
// turn ended right after, so the reply to the steered answer (the summary of
// all four) was never recorded or shown. A completion must not be steered
// into a running auto-notification turn; it is queued as its own turn.
func TestSteerBackgroundAgentCompletionQueuesBehindAnAutoNotificationTurn(t *testing.T) {
	store := internalevents.NewEventStore(10)
	defer store.Stop()

	sessionID := "code-asker"
	api := &StreamingAPI{
		eventStore:       store,
		runningAgents:    map[string]*mcpagent.Agent{sessionID: testCodingAgent(llm.ProviderClaudeCode, "claude-code")},
		runningAgentsMux: sync.RWMutex{},
		agentCancelFuncs: map[string]context.CancelFunc{sessionID: func() {}},
		agentCancelMux:   sync.RWMutex{},
		bgAgentRegistry:  NewBackgroundAgentRegistry(),
		activeSessions:   map[string]*ActiveSessionInfo{sessionID: {SessionID: sessionID, IsSyntheticTurn: true}},
	}
	bg := &BackgroundAgent{ID: "function-call-aska-0003", Name: "Ask activeeyes", SessionID: sessionID, Status: BGAgentCompleted, Result: "answer"}
	api.bgAgentRegistry.Register(sessionID, bg)

	if api.steerBackgroundAgentCompletion(sessionID, bg.ID) {
		t.Fatal("a completion was steered into a running auto-notification turn; it must be queued as its own turn")
	}
	bg.mu.RLock()
	notified := bg.completionNotification == completionNotificationDelivered
	bg.mu.RUnlock()
	if notified {
		t.Fatal("the queued completion was marked delivered")
	}
}

func testCodingAgent(provider llm.Provider, model string) *mcpagent.Agent {
	agent := &mcpagent.Agent{}
	mcpagent.ApplyAgentResumeHandle(agent, &mcpagent.AgentSessionHandle{
		Provider: llmtypes.CodingProviderSessionHandle{Provider: string(provider), Model: model},
	})
	return agent
}
