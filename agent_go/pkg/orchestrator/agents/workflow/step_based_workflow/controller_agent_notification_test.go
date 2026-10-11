package step_based_workflow

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAgentItemNotificationStartAndComplete(t *testing.T) {
	notifier := &recordingExecutionNotifier{}
	orchestrator := &StepBasedWorkflowOrchestrator{
		workshopExecutionNotifier: notifier,
		selectedRunFolder:         "iteration-0",
		currentGroupName:          "default",
	}
	step := &AgentPlanStep{
		CommonStepFields: CommonStepFields{
			ID:    "morning-sequence",
			Title: "Morning sequence",
		},
	}
	item := AgentItem{
		ID:   "write-note",
		Type: "user_message",
		Kind: "execution",
	}

	execID, name, meta, active := orchestrator.startAgentItemNotification(context.Background(), step, item, 2, "step-3", "configured_queue", time.Unix(10, 20))
	if !active {
		t.Fatal("expected notification to be active")
	}
	if len(notifier.starts) != 1 {
		t.Fatalf("expected one start, got %d", len(notifier.starts))
	}
	if notifier.starts[0].ID != execID {
		t.Fatalf("start ID = %q, want %q", notifier.starts[0].ID, execID)
	}
	if notifier.starts[0].Kind != "agent_item" {
		t.Fatalf("start kind = %q, want agent_item", notifier.starts[0].Kind)
	}
	if !strings.Contains(name, "Morning sequence / write-note (user_message)") {
		t.Fatalf("unexpected notification name: %q", name)
	}
	for key, want := range map[string]string{
		"execution_type": "agent-item",
		"step_id":        "morning-sequence",
		"step_title":     "Morning sequence",
		"step_index":     "2",
		"step_path":      "step-3",
		"item_id":        "write-note",
		"item_type":      "user_message",
		"item_kind":      "execution",
		"source":         "configured_queue",
		"run_folder":     "iteration-0",
		"group_name":     "default",
	} {
		if got := meta[key]; got != want {
			t.Fatalf("meta[%s] = %q, want %q", key, got, want)
		}
	}

	orchestrator.completeAgentItemNotification(context.Background(), execID, name, "message write-note succeeded", meta, active, nil)
	if len(notifier.completes) != 1 {
		t.Fatalf("expected one completion, got %d", len(notifier.completes))
	}
	if notifier.completes[0].id != execID {
		t.Fatalf("completion ID = %q, want %q", notifier.completes[0].id, execID)
	}
	if notifier.completes[0].result != "message write-note succeeded" {
		t.Fatalf("completion result = %q", notifier.completes[0].result)
	}
	if notifier.completes[0].err != nil {
		t.Fatalf("unexpected completion error: %v", notifier.completes[0].err)
	}
}
