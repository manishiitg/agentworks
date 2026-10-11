package step_based_workflow

import (
	"context"
	"testing"
	"time"

	orchestrator_events "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	baseevents "github.com/manishiitg/mcpagent/events"
)

type recordingExecutionNotifier struct {
	starts    []WorkshopExecutionStart
	completes []recordedExecutionComplete
}

type recordedExecutionComplete struct {
	id     string
	name   string
	result string
	meta   map[string]string
	err    error
}

func (n *recordingExecutionNotifier) OnExecutionStart(start WorkshopExecutionStart) {
	n.starts = append(n.starts, start)
}

func (n *recordingExecutionNotifier) OnExecutionComplete(execID, name, result string, meta map[string]string, err error) {
	n.completes = append(n.completes, recordedExecutionComplete{
		id:     execID,
		name:   name,
		result: result,
		meta:   meta,
		err:    err,
	})
}

func (n *recordingExecutionNotifier) OnExecutionTerminated(execID, name string) {}

func TestWorkflowProgressBridgeNotifiesStepStartAndCompletion(t *testing.T) {
	notifier := &recordingExecutionNotifier{}
	session := &WorkshopChatSession{
		StepRegistry:      NewWorkshopStepRegistry(),
		executionNotifier: notifier,
	}
	bridge := &workflowProgressBridge{
		session:   session,
		parentID:  "workflow-full-123",
		iteration: "iteration-0",
		groupName: "job-search",
	}

	start := &baseevents.AgentEvent{
		Type:      orchestrator_events.OrchestratorAgentStart,
		Timestamp: time.Now(),
		Data: &orchestrator_events.OrchestratorAgentStartEvent{
			AgentType: "todo_planner_execution",
			AgentName: "search-score-jobs",
			StepIndex: 2,
		},
	}
	if err := bridge.HandleEvent(context.Background(), start); err != nil {
		t.Fatalf("start HandleEvent failed: %v", err)
	}

	end := &baseevents.AgentEvent{
		Type:      orchestrator_events.OrchestratorAgentEnd,
		Timestamp: time.Now(),
		Data: &orchestrator_events.OrchestratorAgentEndEvent{
			AgentType: "todo_planner_execution",
			AgentName: "search-score-jobs",
			StepIndex: 2,
			Result:    "saved 5 candidates",
			Success:   true,
		},
	}
	if err := bridge.HandleEvent(context.Background(), end); err != nil {
		t.Fatalf("end HandleEvent failed: %v", err)
	}

	if len(notifier.starts) != 1 {
		t.Fatalf("expected one start notification, got %d", len(notifier.starts))
	}
	if len(notifier.completes) != 1 {
		t.Fatalf("expected one completion notification, got %d", len(notifier.completes))
	}
	if notifier.starts[0].ID != notifier.completes[0].id {
		t.Fatalf("expected start/end IDs to match, got %q and %q", notifier.starts[0].ID, notifier.completes[0].id)
	}
	if got := notifier.starts[0].Metadata["step_id"]; got != "search-score-jobs" {
		t.Fatalf("expected canonical step ID on start, got %q", got)
	}
	if got := notifier.completes[0].meta["step_id"]; got != "search-score-jobs" {
		t.Fatalf("expected canonical step ID on completion, got %q", got)
	}
	if got := notifier.completes[0].meta["group_name"]; got != "job-search" {
		t.Fatalf("expected group metadata, got %q", got)
	}
	if got := notifier.completes[0].meta["iteration"]; got != "iteration-0" {
		t.Fatalf("expected iteration metadata, got %q", got)
	}
	if exec := session.StepRegistry.Get(notifier.completes[0].id); exec == nil {
		t.Fatal("expected progress execution to be registered")
	}
}

func TestWorkflowProgressBridgeWaitsForOrchestratorStepCompletion(t *testing.T) {
	notifier := &recordingExecutionNotifier{}
	session := &WorkshopChatSession{
		StepRegistry:      NewWorkshopStepRegistry(),
		executionNotifier: notifier,
	}
	bridge := &workflowProgressBridge{
		session:   session,
		parentID:  "workflow-full-123",
		iteration: "iteration-0",
		groupName: "default",
	}

	start := &baseevents.AgentEvent{
		Type:      orchestrator_events.OrchestratorAgentStart,
		Timestamp: time.Now(),
		Data: &orchestrator_events.OrchestratorAgentStartEvent{
			AgentType: "delegating_agent",
			AgentName: "[Route] Execution",
			StepIndex: 6,
		},
	}
	if err := bridge.HandleEvent(context.Background(), start); err != nil {
		t.Fatalf("start HandleEvent failed: %v", err)
	}

	// This is only the end of the LLM turn that launched an asynchronous child.
	// The workflow step must remain running until the controller has waited for
	// and reconciled that child.
	turnEnd := &baseevents.AgentEvent{
		Type:      orchestrator_events.OrchestratorAgentEnd,
		Timestamp: time.Now(),
		Data: &orchestrator_events.OrchestratorAgentEndEvent{
			AgentType: "delegating_agent",
			AgentName: "[Route] Execution",
			StepIndex: 6,
			Result:    "execute-allocate was dispatched asynchronously; waiting for completion",
			Success:   true,
		},
	}
	if err := bridge.HandleEvent(context.Background(), turnEnd); err != nil {
		t.Fatalf("turn-end HandleEvent failed: %v", err)
	}

	if len(notifier.completes) != 0 {
		t.Fatalf("todo-task LLM turn end emitted %d premature completion(s)", len(notifier.completes))
	}
	if len(notifier.starts) != 1 {
		t.Fatalf("expected one running execution, got %d starts", len(notifier.starts))
	}
	if exec := session.StepRegistry.Get(notifier.starts[0].ID); exec == nil || exec.Status != WorkshopStepRunning {
		t.Fatalf("todo-task execution should remain running after turn end, got %#v", exec)
	}

	stepCompleted := &baseevents.AgentEvent{
		Type:      orchestrator_events.AgentStepCompleted,
		Timestamp: time.Now(),
		Data: &AgentStepCompletedEvent{
			StepIndex:        6,
			StepID:           "step-execution-pipeline",
			StepTitle:        "[Route] Execution",
			CompletionReason: "all asynchronous routes completed and reconciled",
		},
	}
	if err := bridge.HandleEvent(context.Background(), stepCompleted); err != nil {
		t.Fatalf("todo-task completion HandleEvent failed: %v", err)
	}

	if len(notifier.completes) != 1 {
		t.Fatalf("expected one authoritative completion, got %d", len(notifier.completes))
	}
	if notifier.starts[0].ID != notifier.completes[0].id {
		t.Fatalf("expected start/completion IDs to match, got %q and %q", notifier.starts[0].ID, notifier.completes[0].id)
	}
	if got := notifier.completes[0].meta["step_id"]; got != "step-execution-pipeline" {
		t.Fatalf("step_id metadata = %q, want step-execution-pipeline", got)
	}
	if exec := session.StepRegistry.Get(notifier.completes[0].id); exec == nil || exec.Status != WorkshopStepDone {
		t.Fatalf("todo-task execution should be done after authoritative completion, got %#v", exec)
	}
}

func TestWorkflowProgressBridgeNotifiesCompletionWithoutStart(t *testing.T) {
	notifier := &recordingExecutionNotifier{}
	session := &WorkshopChatSession{
		StepRegistry:      NewWorkshopStepRegistry(),
		executionNotifier: notifier,
	}
	bridge := &workflowProgressBridge{
		session:  session,
		parentID: "workflow-full-123",
	}

	end := &baseevents.AgentEvent{
		Type:      orchestrator_events.OrchestratorAgentEnd,
		Timestamp: time.Now(),
		Data: &orchestrator_events.OrchestratorAgentEndEvent{
			AgentType: "generic_execution",
			AgentName: "prepare-route-input",
			StepIndex: 1,
			Error:     "route failed",
			Success:   false,
		},
	}
	if err := bridge.HandleEvent(context.Background(), end); err != nil {
		t.Fatalf("end HandleEvent failed: %v", err)
	}

	if len(notifier.starts) != 1 {
		t.Fatalf("expected synthesized start notification, got %d", len(notifier.starts))
	}
	if len(notifier.completes) != 1 {
		t.Fatalf("expected one completion notification, got %d", len(notifier.completes))
	}
	if notifier.completes[0].err == nil {
		t.Fatal("expected failure error")
	}
}

func TestWorkflowProgressBridgeSkipsAgentItemAgents(t *testing.T) {
	notifier := &recordingExecutionNotifier{}
	session := &WorkshopChatSession{
		StepRegistry:      NewWorkshopStepRegistry(),
		executionNotifier: notifier,
	}
	bridge := &workflowProgressBridge{
		session:  session,
		parentID: "workflow-full-123",
	}

	for _, event := range []*baseevents.AgentEvent{
		{
			Type:      orchestrator_events.OrchestratorAgentStart,
			Timestamp: time.Now(),
			Data: &orchestrator_events.OrchestratorAgentStartEvent{
				AgentType: "todo_planner_execution",
				AgentName: "agent-step-1-item-1",
				StepIndex: 1,
			},
		},
		{
			Type:      orchestrator_events.OrchestratorAgentEnd,
			Timestamp: time.Now(),
			Data: &orchestrator_events.OrchestratorAgentEndEvent{
				AgentType: "todo_planner_execution",
				AgentName: "agent-step-1-item-1",
				StepIndex: 1,
				Result:    "done",
				Success:   true,
			},
		},
	} {
		if err := bridge.HandleEvent(context.Background(), event); err != nil {
			t.Fatalf("HandleEvent failed: %v", err)
		}
	}

	if len(notifier.starts) != 0 || len(notifier.completes) != 0 {
		t.Fatalf("expected agent agent bridge notifications to be skipped, got starts=%d completes=%d", len(notifier.starts), len(notifier.completes))
	}
}
