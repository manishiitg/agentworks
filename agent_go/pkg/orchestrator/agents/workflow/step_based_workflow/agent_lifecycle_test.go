package step_based_workflow

import (
	"context"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"testing"
	"time"
)

func TestAgentFullRunRegistersParentBeforeItems(t *testing.T) {
	notifier := &recordingExecutionNotifier{}
	registry := NewWorkshopStepRegistry()
	controller := &StepBasedWorkflowOrchestrator{workshopExecutionNotifier: notifier, workshopStepRegistry: registry}
	step := &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "sequence", Title: "Sequence"}}
	root := virtualtools.WithBackgroundAgentID(context.Background(), "workflow-full-test")
	ctx, finish := controller.beginAgentExecution(root, step)
	if len(notifier.starts) != 1 || notifier.starts[0].ParentExecutionID != "workflow-full-test" {
		t.Fatal("sequence parent not registered under the full run")
	}
	parentID := notifier.starts[0].ID
	if registry.Get(parentID) == nil || currentWorkshopParentExecutionID(ctx) != parentID {
		t.Fatal("sequence context and registry disagree")
	}
	controller.startAgentItemNotification(ctx, step, AgentItem{ID: "validate", Type: "prevalidation"}, 0, "step-1", "configured_queue", time.Now())
	if notifier.starts[1].ParentExecutionID != parentID {
		t.Fatal("item not owned by registered sequence")
	}
	finish("done", nil)
	if registry.Get(parentID).Snapshot().Status != WorkshopStepDone || len(notifier.completes) != 1 || notifier.completes[0].id != parentID {
		t.Fatal("sequence completion did not settle its parent")
	}
}

func TestAgentStandaloneKeepsExistingLifecycle(t *testing.T) {
	notifier := &recordingExecutionNotifier{}
	controller := &StepBasedWorkflowOrchestrator{workshopExecutionNotifier: notifier}
	step := &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "sequence"}}
	root := virtualtools.WithBackgroundAgentID(context.Background(), "exec-sequence-123")
	ctx, finish := controller.beginAgentExecution(root, step)
	finish("done", nil)
	if ctx != root || len(notifier.starts) != 0 || len(notifier.completes) != 0 {
		t.Fatal("standalone execution got a duplicate lifecycle")
	}
}

func TestWorkshopCancelPublishesBeforeCancelingContext(t *testing.T) {
	registry := NewWorkshopStepRegistry()
	published := false
	exec := &WorkshopStepExecution{ID: "step", Status: WorkshopStepRunning, cancel: func() {
		if !published {
			t.Error("worker unwound before cancellation was published")
		}
	}}
	registry.Register(exec)
	_, err := registry.cancelWithNotification("step", func(snapshot WorkshopStepSnapshot) {
		if snapshot.Status != WorkshopStepCancelled {
			t.Error("stop published before registry settled")
		}
		published = true
	})
	if err != nil || !published {
		t.Fatalf("cancellation failed: %v", err)
	}
}

func TestWorkshopCancelAllSettlesEveryEntryBeforeUnwinding(t *testing.T) {
	registry := NewWorkshopStepRegistry()
	var entries []*WorkshopStepExecution
	for _, id := range []string{"step", "child"} {
		exec := &WorkshopStepExecution{ID: id, Status: WorkshopStepRunning}
		exec.cancel = func() {
			for _, entry := range entries {
				if entry.Snapshot().Status != WorkshopStepCancelled {
					t.Errorf("%s was still running during cancellation", entry.ID)
				}
			}
		}
		registry.Register(exec)
		entries = append(entries, exec)
	}
	notifications := 0
	stopped := registry.cancelAllWithNotification(func(WorkshopStepSnapshot) { notifications++ })
	if len(stopped) != 2 || notifications != 2 {
		t.Fatalf("stopped=%d, notifications=%d", len(stopped), notifications)
	}
}
