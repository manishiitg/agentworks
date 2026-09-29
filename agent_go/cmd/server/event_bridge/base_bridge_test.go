package eventbridge

import (
	"context"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	pkgevents "github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// TestHandleEventSurfacesOrchestratorAgentErrorText is the real-pipeline twin
// of scheduledTurnFailure's own tests (scheduled_turn_outcome_test.go), which
// hand-construct events.Event with Error already populated and so never
// exercised the conversion this bridge performs.
//
// A step-execution failure reaches the scheduler as an orchestrator_agent_error
// event whose typed payload (OrchestratorAgentErrorEvent.Error) carries the
// real failure text, e.g. "...claudecode/... [quota_exhausted]...". Before this
// fix, HandleEvent never copied that into the stored Event's top-level Error
// field, so scheduledTurnFailure always fell back to the generic
// "turn ended with orchestrator_agent_error" — losing the quota_exhausted
// marker classifyCapacityWait/turnLevelCapacityWait match on, so a transient
// capacity wall (PLAT-101) was recorded as a hard failure instead of
// suspending and auto-resuming.
func TestHandleEventSurfacesOrchestratorAgentErrorText(t *testing.T) {
	store := events.NewEventStore(200)
	bridge := &BaseEventBridge{
		EventStore: store,
		SessionID:  "sched-session",
		Logger:     loggerv2.NewNoop(),
		BridgeName: "test_bridge",
	}

	const wantError = "all LLMs failed (primary + 0 fallbacks): claude-code/claude-sonnet-5-5 [quota_exhausted]: claude code usage limit reached"

	agentEvent := &pkgevents.AgentEvent{
		Type: orchestratorevents.OrchestratorAgentError,
		Data: &orchestratorevents.OrchestratorAgentErrorEvent{
			AgentType: "workflow",
			AgentName: "message-sequence-step-execution",
			Error:     wantError,
		},
	}

	if err := bridge.HandleEvent(context.Background(), agentEvent); err != nil {
		t.Fatalf("HandleEvent returned an error: %v", err)
	}

	result := store.GetEvents("sched-session", events.GetEventsOptions{})
	if !result.Exists || len(result.Events) != 1 {
		t.Fatalf("expected exactly one stored event, got exists=%v count=%d", result.Exists, len(result.Events))
	}
	if got := result.Events[0].Error; got != wantError {
		t.Errorf("stored Event.Error = %q, want %q — the typed payload's error text never reached the field scheduledTurnFailure reads", got, wantError)
	}
}

// TestHandleEventSurfacesAgentAndConversationErrorText covers the other two
// event types scheduledTurnFailureEventTypes matches on.
func TestHandleEventSurfacesAgentAndConversationErrorText(t *testing.T) {
	cases := []struct {
		name  string
		event *pkgevents.AgentEvent
	}{
		{
			name: "agent_error",
			event: &pkgevents.AgentEvent{
				Type: pkgevents.AgentError,
				Data: &pkgevents.AgentErrorEvent{Error: "agent-level failure"},
			},
		},
		{
			name: "conversation_error",
			event: &pkgevents.AgentEvent{
				Type: pkgevents.ConversationError,
				Data: &pkgevents.ConversationErrorEvent{Error: "conversation-level failure"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := events.NewEventStore(200)
			bridge := &BaseEventBridge{
				EventStore: store,
				SessionID:  "sched-session",
				Logger:     loggerv2.NewNoop(),
				BridgeName: "test_bridge",
			}
			if err := bridge.HandleEvent(context.Background(), tc.event); err != nil {
				t.Fatalf("HandleEvent returned an error: %v", err)
			}
			result := store.GetEvents("sched-session", events.GetEventsOptions{})
			if !result.Exists || len(result.Events) != 1 {
				t.Fatalf("expected exactly one stored event, got exists=%v count=%d", result.Exists, len(result.Events))
			}
			if got := result.Events[0].Error; got == "" {
				t.Errorf("stored Event.Error was left blank for a %s event", tc.name)
			}
		})
	}
}
