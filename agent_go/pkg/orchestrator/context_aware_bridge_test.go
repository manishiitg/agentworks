package orchestrator

import (
	"context"
	"testing"
	"time"

	orchevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	mcpagent_events "github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

type captureEventListener struct {
	event *mcpagent_events.AgentEvent
}

func (l *captureEventListener) Name() string { return "capture" }

func (l *captureEventListener) HandleEvent(ctx context.Context, event *mcpagent_events.AgentEvent) error {
	l.event = event
	return nil
}

func TestContextAwareBridgeTagsTerminalStreamWithExecutionOwner(t *testing.T) {
	listener := &captureEventListener{}
	bridge := NewContextAwareEventBridge(listener, loggerv2.NewNoop())
	bridge.SetCurrentStepContext("shared-step", "todo_task")

	ctx := context.WithValue(context.Background(), orchevents.ParentExecutionIDKey, "sub-exec-rds-evidence-123")
	event := &mcpagent_events.AgentEvent{
		Type:      mcpagent_events.StreamingChunk,
		Timestamp: time.Now(),
		Data: &mcpagent_events.StreamingChunkEvent{
			BaseEventData: mcpagent_events.BaseEventData{
				Timestamp: time.Now(),
				Metadata: map[string]interface{}{
					"kind":           "terminal",
					"execution_kind": "main_agent",
				},
			},
			Content:    "terminal snapshot",
			ChunkIndex: 1,
		},
	}

	if err := bridge.HandleEvent(ctx, event); err != nil {
		t.Fatalf("HandleEvent returned error: %v", err)
	}
	if listener.event == nil {
		t.Fatal("event was not forwarded")
	}
	chunk, ok := listener.event.Data.(*mcpagent_events.StreamingChunkEvent)
	if !ok {
		t.Fatalf("forwarded event data = %T, want *StreamingChunkEvent", listener.event.Data)
	}
	metadata := chunk.GetBaseEventData().Metadata
	if got := metadata["execution_owner_id"]; got != "sub-exec-rds-evidence-123" {
		t.Fatalf("execution_owner_id = %v, want sub-exec-rds-evidence-123", got)
	}
	if got := metadata["background_agent_id"]; got != "sub-exec-rds-evidence-123" {
		t.Fatalf("background_agent_id = %v, want sub-exec-rds-evidence-123", got)
	}
	if got := metadata["execution_kind"]; got != "background_agent" {
		t.Fatalf("execution_kind = %v, want background_agent", got)
	}
	if got := metadata["scope"]; got != "background_agent" {
		t.Fatalf("scope = %v, want background_agent", got)
	}
	if got := metadata["current_step_id"]; got != "shared-step" {
		t.Fatalf("current_step_id = %v, want shared-step", got)
	}
	if got := metadata["current_step_type"]; got != "todo_task" {
		t.Fatalf("current_step_type = %v, want todo_task", got)
	}
	if got := metadata["plan_step_type"]; got != "todo_task" {
		t.Fatalf("plan_step_type = %v, want todo_task", got)
	}
}

func TestContextAwareBridgePushContextRichTagsTerminalStreamWithStepType(t *testing.T) {
	listener := &captureEventListener{}
	bridge := NewContextAwareEventBridge(listener, loggerv2.NewNoop())
	bridge.SetCurrentStepContext("parent-orchestrator", "todo_task")
	bridge.PushContextRich("execution", 2, "route-writer", "Route writer", RichStepContext{
		StepName:     "Route writer",
		StepType:     "agent",
		ParentStepID: "parent-orchestrator",
		TriggeredBy:  "todo_task_route",
	})

	event := &mcpagent_events.AgentEvent{
		Type:      mcpagent_events.StreamingChunk,
		Timestamp: time.Now(),
		Data: &mcpagent_events.StreamingChunkEvent{
			BaseEventData: mcpagent_events.BaseEventData{
				Timestamp: time.Now(),
				Metadata: map[string]interface{}{
					"kind": "terminal",
				},
			},
			Content:    "route terminal snapshot",
			ChunkIndex: 1,
		},
	}

	if err := bridge.HandleEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleEvent returned error: %v", err)
	}
	chunk, ok := listener.event.Data.(*mcpagent_events.StreamingChunkEvent)
	if !ok {
		t.Fatalf("forwarded event data = %T, want *StreamingChunkEvent", listener.event.Data)
	}
	metadata := chunk.GetBaseEventData().Metadata
	if got := metadata["current_step_id"]; got != "route-writer" {
		t.Fatalf("current_step_id = %v, want route-writer", got)
	}
	if got := metadata["parent_step_id"]; got != "parent-orchestrator" {
		t.Fatalf("parent_step_id = %v, want parent-orchestrator", got)
	}
	if got := metadata["current_step_type"]; got != "agent" {
		t.Fatalf("current_step_type = %v, want agent", got)
	}
	if got := metadata["plan_step_type"]; got != "agent" {
		t.Fatalf("plan_step_type = %v, want agent", got)
	}
}

func TestContextAwareBridgeClassifiesStructuredExecutionPrompt(t *testing.T) {
	listener := &captureEventListener{}
	bridge := NewContextAwareEventBridge(listener, loggerv2.NewNoop())
	bridge.SetOrchestratorContext("execution", 0, "write-report", "Write report")
	bridge.SetRichStepContext(RichStepContext{
		StepName:  "Write report",
		StepType:  "agent",
		Transport: "structured",
	})

	event := &mcpagent_events.AgentEvent{
		Type:      mcpagent_events.UserMessage,
		Timestamp: time.Now(),
		Data:      mcpagent_events.NewUserMessageEvent(0, "generated executor instructions", "user"),
	}

	if err := bridge.HandleEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleEvent returned error: %v", err)
	}
	message, ok := listener.event.Data.(*mcpagent_events.UserMessageEvent)
	if !ok {
		t.Fatalf("forwarded event data = %T, want *UserMessageEvent", listener.event.Data)
	}
	if got := message.Metadata["source"]; got != "execution_prompt" {
		t.Fatalf("metadata source = %v, want execution_prompt", got)
	}
}

func TestContextAwareBridgePreservesExplicitUserMessageSource(t *testing.T) {
	listener := &captureEventListener{}
	bridge := NewContextAwareEventBridge(listener, loggerv2.NewNoop())
	bridge.SetOrchestratorContext("execution", 0, "write-report", "Write report")
	bridge.SetRichStepContext(RichStepContext{
		StepName:  "Write report",
		StepType:  "agent",
		Transport: "structured",
	})

	message := mcpagent_events.NewUserMessageEvent(0, "human follow-up", "user")
	message.Metadata = map[string]interface{}{"source": "coding_agent_live_input"}
	event := &mcpagent_events.AgentEvent{
		Type:      mcpagent_events.UserMessage,
		Timestamp: time.Now(),
		Data:      message,
	}

	if err := bridge.HandleEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleEvent returned error: %v", err)
	}
	forwarded := listener.event.Data.(*mcpagent_events.UserMessageEvent)
	if got := forwarded.Metadata["source"]; got != "coding_agent_live_input" {
		t.Fatalf("metadata source = %v, want coding_agent_live_input", got)
	}
}

func TestContextAwareBridgeUsesExecutionLocalContextForParallelChildren(t *testing.T) {
	listener := &captureEventListener{}
	bridge := NewContextAwareEventBridge(listener, loggerv2.NewNoop())
	bridge.SetOrchestratorContext("execution", 0, "global-parent", "parent")

	ctxA := WithEventContextOverride(context.Background(), "execution", 1, "child-a", "Child A", RichStepContext{
		StepName:     "Child A",
		StepType:     "regular",
		ParentStepID: "todo-parent",
		TriggeredBy:  "todo_task_route",
	})
	ctxB := WithEventContextOverride(context.Background(), "execution", 1, "child-b", "Child B", RichStepContext{
		StepName:     "Child B",
		StepType:     "agent",
		ParentStepID: "nested-todo",
		TriggeredBy:  "todo_task_route",
	})

	assertContext := func(ctx context.Context, wantID, wantType, wantParent string) {
		t.Helper()
		event := &mcpagent_events.AgentEvent{
			Type:      mcpagent_events.StreamingChunk,
			Timestamp: time.Now(),
			Data: &mcpagent_events.StreamingChunkEvent{
				BaseEventData: mcpagent_events.BaseEventData{Timestamp: time.Now()},
				Content:       "child output",
			},
		}
		if err := bridge.HandleEvent(ctx, event); err != nil {
			t.Fatalf("HandleEvent returned error: %v", err)
		}
		metadata := listener.event.Data.(*mcpagent_events.StreamingChunkEvent).GetBaseEventData().Metadata
		if got := metadata["current_step_id"]; got != wantID {
			t.Fatalf("current_step_id = %v, want %s", got, wantID)
		}
		if got := metadata["current_step_type"]; got != wantType {
			t.Fatalf("current_step_type = %v, want %s", got, wantType)
		}
		if got := metadata["parent_step_id"]; got != wantParent {
			t.Fatalf("parent_step_id = %v, want %s", got, wantParent)
		}
	}

	// Interleave the two contexts. Neither event may inherit the mutable global
	// bridge state or the other child's metadata.
	assertContext(ctxA, "child-a", "regular", "todo-parent")
	bridge.SetOrchestratorContext("execution", 9, "unrelated-global", "other")
	assertContext(ctxB, "child-b", "agent", "nested-todo")
	assertContext(ctxA, "child-a", "regular", "todo-parent")
}

func TestContextAwareBridgeTimingCaptureIsExecutionLocal(t *testing.T) {
	bridge := NewContextAwareEventBridge(&captureEventListener{}, loggerv2.NewNoop())
	ctxA := WithEventContextOverride(context.Background(), "execution", 0, "child-a", "Child A", RichStepContext{})
	ctxB := WithEventContextOverride(context.Background(), "execution", 0, "child-b", "Child B", RichStepContext{})
	ctxA = bridge.StartTimingCaptureFor(ctxA)
	ctxB = bridge.StartTimingCaptureFor(ctxB)

	emitTool := func(ctx context.Context, id string) {
		t.Helper()
		start := &mcpagent_events.AgentEvent{
			Type:      mcpagent_events.ToolCallStart,
			Timestamp: time.Now(),
			Data: &mcpagent_events.ToolCallStartEvent{
				BaseEventData: mcpagent_events.BaseEventData{Timestamp: time.Now()},
				ToolCallID:    id,
				ToolName:      "execute_shell_command",
				ToolParams:    mcpagent_events.ToolParams{Arguments: id},
			},
		}
		if err := bridge.HandleEvent(ctx, start); err != nil {
			t.Fatalf("start HandleEvent returned error: %v", err)
		}
		end := &mcpagent_events.AgentEvent{
			Type:      mcpagent_events.ToolCallEnd,
			Timestamp: time.Now(),
			Data: &mcpagent_events.ToolCallEndEvent{
				BaseEventData: mcpagent_events.BaseEventData{Timestamp: time.Now()},
				ToolCallID:    id,
				ToolName:      "execute_shell_command",
				Result:        id + "-result",
			},
		}
		if err := bridge.HandleEvent(ctx, end); err != nil {
			t.Fatalf("end HandleEvent returned error: %v", err)
		}
	}

	emitTool(ctxA, "tool-a")
	emitTool(ctxB, "tool-b")
	captureB := bridge.DrainTimingCaptureFor(ctxB)
	captureA := bridge.DrainTimingCaptureFor(ctxA)

	if len(captureA.ToolCalls) != 1 || captureA.ToolCalls[0].ToolCallID != "tool-a" || captureA.ToolCalls[0].StepID != "child-a" {
		t.Fatalf("capture A = %+v, want only tool-a for child-a", captureA.ToolCalls)
	}
	if len(captureB.ToolCalls) != 1 || captureB.ToolCalls[0].ToolCallID != "tool-b" || captureB.ToolCalls[0].StepID != "child-b" {
		t.Fatalf("capture B = %+v, want only tool-b for child-b", captureB.ToolCalls)
	}
}
