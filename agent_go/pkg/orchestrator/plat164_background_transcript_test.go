package orchestrator

import (
	"context"
	"sync"
	"testing"
	"time"

	orchevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

// recordingTranscriptWriter captures every AppendBackgroundAgentTranscriptEvent
// call, mirroring recordingTokenPersister's shape in
// workflow_cost_e2e_real_test.go.
type recordingTranscriptWriter struct {
	mu    sync.Mutex
	calls []transcriptCall
}

type transcriptCall struct {
	sessionID string
	agentID   string
	evt       orchevents.BackgroundAgentTranscriptEvent
}

func (w *recordingTranscriptWriter) AppendBackgroundAgentTranscriptEvents(_ context.Context, sessionID, agentID string, evts []orchevents.BackgroundAgentTranscriptEvent) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, evt := range evts {
		w.calls = append(w.calls, transcriptCall{sessionID: sessionID, agentID: agentID, evt: evt})
	}
	return nil
}

func (w *recordingTranscriptWriter) snapshot() []transcriptCall {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]transcriptCall(nil), w.calls...)
}

func (w *recordingTranscriptWriter) waitForCall(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(w.snapshot()) > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for transcript writer call")
}

func newTestBridgeWithTranscriptWriter() (*ContextAwareEventBridge, *recordingTranscriptWriter) {
	bridge := NewContextAwareEventBridge(noopListener{}, silentLoggerV2{})
	writer := &recordingTranscriptWriter{}
	bridge.SetBackgroundAgentTranscriptWriter(writer)
	return bridge, writer
}

// TestBackgroundAgentTranscriptAppendedForBackgroundExecution proves the
// PLAT-164 wiring: a user message carrying a ParentExecutionIDKey that is
// NOT a "workflow-step:"-prefixed id (i.e. a background agent, matching the
// same execution_kind classification HandleEvent already tags into
// metadata) reaches the transcript writer, keyed by that execution id.
func TestBackgroundAgentTranscriptAppendedForBackgroundExecution(t *testing.T) {
	bridge, writer := newTestBridgeWithTranscriptWriter()

	ctx := context.WithValue(context.Background(), orchevents.ParentExecutionIDKey, "workshop-background-task-abc")
	err := bridge.HandleEvent(ctx, &unifiedevents.AgentEvent{
		Type:      unifiedevents.UserMessage,
		Timestamp: time.Now(),
		SessionID: "session-1",
		Component: "test",
		Data: &unifiedevents.UserMessageEvent{
			BaseEventData: unifiedevents.BaseEventData{Timestamp: time.Now()},
			Content:       "please investigate the drop",
			Role:          "user",
		},
	})
	if err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}

	writer.waitForCall(t, 3*time.Second)
	calls := writer.snapshot()
	if len(calls) != 1 {
		t.Fatalf("writer calls = %d, want 1", len(calls))
	}
	if calls[0].sessionID != "session-1" {
		t.Fatalf("sessionID = %q, want session-1", calls[0].sessionID)
	}
	if calls[0].agentID != "workshop-background-task-abc" {
		t.Fatalf("agentID = %q, want workshop-background-task-abc", calls[0].agentID)
	}
	if calls[0].evt.Text != "please investigate the drop" {
		t.Fatalf("evt.Text = %q", calls[0].evt.Text)
	}
}

// TestBackgroundAgentTranscriptSkippedForWorkflowStep proves the writer is
// NOT invoked for a workflow-step-owned execution: workflow steps already
// get a durable per-run record for free (runs/iteration-N/...), and this
// scope is exactly the same "workflow-step:" prefix check HandleEvent uses
// to classify execution_kind as "workflow_step" instead of
// "background_agent" in event metadata.
func TestBackgroundAgentTranscriptSkippedForWorkflowStep(t *testing.T) {
	bridge, writer := newTestBridgeWithTranscriptWriter()

	ctx := context.WithValue(context.Background(), orchevents.ParentExecutionIDKey, "workflow-step:fetch-data")
	err := bridge.HandleEvent(ctx, &unifiedevents.AgentEvent{
		Type:      unifiedevents.UserMessage,
		Timestamp: time.Now(),
		SessionID: "session-1",
		Component: "test",
		Data: &unifiedevents.UserMessageEvent{
			BaseEventData: unifiedevents.BaseEventData{Timestamp: time.Now()},
			Content:       "step-scoped prompt",
			Role:          "user",
		},
	})
	if err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}

	// Give any (incorrect) async call a chance to land before asserting none did.
	time.Sleep(50 * time.Millisecond)
	if calls := writer.snapshot(); len(calls) != 0 {
		t.Fatalf("writer calls = %d, want 0 for a workflow-step execution: %+v", len(calls), calls)
	}
}

// TestBackgroundAgentTranscriptSkippedWithNoExecutionOwner proves a
// top-level chat/workflow-builder turn (no ParentExecutionIDKey at all)
// does not get a transcript — it already has its own conversation
// persistence (workflowBuilderConversationLogPath).
func TestBackgroundAgentTranscriptSkippedWithNoExecutionOwner(t *testing.T) {
	bridge, writer := newTestBridgeWithTranscriptWriter()

	err := bridge.HandleEvent(context.Background(), &unifiedevents.AgentEvent{
		Type:      unifiedevents.UserMessage,
		Timestamp: time.Now(),
		SessionID: "session-1",
		Component: "test",
		Data: &unifiedevents.UserMessageEvent{
			BaseEventData: unifiedevents.BaseEventData{Timestamp: time.Now()},
			Content:       "top-level chat prompt",
			Role:          "user",
		},
	})
	if err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	if calls := writer.snapshot(); len(calls) != 0 {
		t.Fatalf("writer calls = %d, want 0 with no execution owner: %+v", len(calls), calls)
	}
}

func TestWaitForBackgroundTranscriptPersistenceDrainsTrackedWrites(t *testing.T) {
	bridge := &ContextAwareEventBridge{}
	finished := make(chan struct{})
	bridge.persistTranscriptAsync("test", func(context.Context) error {
		time.Sleep(20 * time.Millisecond)
		close(finished)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := bridge.WaitForBackgroundTranscriptPersistence(ctx); err != nil {
		t.Fatalf("wait failed: %v", err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("wait returned before persistence finished")
	}
}
