package server

import (
	"context"
	"testing"
	"time"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"
)

func TestBackgroundNotificationOwnershipPreservesInitiatingSession(t *testing.T) {
	api := lifecycleTestAPI()
	api.trackedWorkflowExecutions["schedule-root"] = &TrackedWorkflowExecution{SessionID: "schedule-cron--job_1", Status: trackedExecutionStatusCompleted}
	api.trackedWorkflowExecutions["chat-root"] = &TrackedWorkflowExecution{SessionID: "chat", Status: trackedExecutionStatusCompleted}
	api.trackedWorkflowExecutions["new-chat-turn"] = &TrackedWorkflowExecution{SessionID: "chat", Source: trackedExecutionSourceConversationTurn, Status: trackedExecutionStatusRunning, StartedAt: time.Now()}
	for _, tc := range []struct {
		name, session, owner, parent string
		want                         bool
	}{
		{"scheduled owner", "schedule-cron--job_1", "schedule-cron--job_1", "schedule-root", true},
		{"foreign scheduled owner", "chat", "schedule-cron--job_1", "schedule-root", false},
		{"foreign parent despite wrong registry bucket", "chat", "chat", "schedule-root", false},
		{"chat work after another human turn", "chat", "chat", "chat-root", true},
		{"foreign session root", "chat", "chat", "session:other-chat", false},
		{"legacy owner without tracked parent", "chat", "chat", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := api.backgroundNotificationOwnedBySession(tc.session, BackgroundAgentSnapshot{SessionID: tc.owner, ParentExecutionID: tc.parent})
			if got != tc.want {
				t.Fatalf("owned = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestForeignCompletionCannotSteerOrEnterNotificationBatch(t *testing.T) {
	api := lifecycleTestAPI()
	agent := testCodingAgent(llm.ProviderCodexCLI, "codex-cli")
	api.runningAgents = map[string]*mcpagent.Agent{"chat": agent}
	api.agentCancelFuncs = map[string]context.CancelFunc{"chat": func() {}}
	api.trackedWorkflowExecutions["schedule-root"] = &TrackedWorkflowExecution{SessionID: "schedule-cron--job_1"}
	called := false
	api.internalUserMessageDeliveryHandler = func(context.Context, *mcpagent.Agent, mcpagent.UserMessageDeliveryRequest) (mcpagent.UserMessageDeliveryResult, error) {
		called = true
		return mcpagent.UserMessageDeliveryResult{}, nil
	}
	// Simulate a legacy/misrouted registration with a known foreign launch parent.
	foreign := &BackgroundAgent{ID: "foreign", SessionID: "chat", ParentExecutionID: "schedule-root", Status: BGAgentCompleted}
	own := &BackgroundAgent{ID: "own", SessionID: "chat", Status: BGAgentCompleted}
	api.bgAgentRegistry.Register("chat", foreign)
	api.bgAgentRegistry.Register("chat", own)
	if !api.steerBackgroundAgentCompletion("chat", foreign.ID) {
		t.Fatal("foreign completion must be consumed without a retry into this chat")
	}
	if called {
		t.Fatal("foreign completion was delivered into the running chat")
	}
	ids := api.filterSupersededCompletions("chat", []string{foreign.ID, own.ID})
	if len(ids) != 1 || ids[0] != own.ID {
		t.Fatalf("batch = %v, want only chat-owned work", ids)
	}
	api.processBackgroundAgentCompletion("chat", foreign.ID)
	api.processBatchedBackgroundAgentCompletions("chat", []string{foreign.ID})
	if called {
		t.Fatal("queued completion delivered into unrelated chat")
	}
}
