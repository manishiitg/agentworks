package server

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestWorkshopTerminationCancelsOwnedTreeBeforeWorkersUnwind(t *testing.T) {
	registry := NewBackgroundAgentRegistry()
	notifier := &workshopExecutionBgNotifier{api: &StreamingAPI{bgAgentRegistry: registry}, sessionID: "tree-stop"}
	const session = "tree-stop"
	parent := &BackgroundAgent{ID: "step", Status: BGAgentRunning, CreatedAt: time.Now()}
	child := &BackgroundAgent{ID: "item", ParentExecutionID: "step", Kind: "agent_item", Status: BGAgentRunning, CreatedAt: time.Now()}
	grandchild := &BackgroundAgent{ID: "worker", ParentExecutionID: "item", Status: BGAgentRunning, CreatedAt: time.Now()}
	sibling := &BackgroundAgent{ID: "other-test", Status: BGAgentRunning, CreatedAt: time.Now()}
	completed := &BackgroundAgent{ID: "previous-item", ParentExecutionID: "step", Status: BGAgentCompleted, CreatedAt: time.Now()}
	for _, agent := range []*BackgroundAgent{parent, child, grandchild, sibling, completed} {
		registry.Register(session, agent)
	}
	calls := 0
	parent.cancel = func() {
		calls++
		for _, agent := range []*BackgroundAgent{parent, child, grandchild} {
			if agent.GetStatus() != BGAgentCanceled {
				t.Errorf("%s still live during cancellation", agent.ID)
			}
		}
		notifier.OnExecutionComplete(child.ID, "item", "", nil, context.Canceled)
	}
	ch := registry.GetNotificationChannel(session)
	notifier.OnExecutionTerminated(parent.ID, "step")
	if calls != 1 {
		t.Fatalf("cancel called %d times", calls)
	}
	if sibling.GetStatus() != BGAgentRunning || completed.GetStatus() != BGAgentCompleted {
		t.Fatal("unrelated or completed work was changed")
	}
	if completed.beginCompletionNotification() {
		t.Fatal("queued reply from an earlier item survived its parent's stop")
	}
	notifier.OnExecutionComplete(child.ID, "item", "late output", nil, nil)
	notifier.OnExecutionTerminated(parent.ID, "step")
	if calls != 1 {
		t.Fatal("repeated stop called cancel again")
	}
	select {
	case id := <-ch:
		t.Fatalf("canceled execution queued a synthetic reply: %s", id)
	default:
	}
}

func TestBackgroundRegistrationInheritsCanceledAncestor(t *testing.T) {
	registry := NewBackgroundAgentRegistry()
	registry.Register("s", &BackgroundAgent{ID: "root", Status: BGAgentCanceled})
	registry.Register("s", &BackgroundAgent{ID: "middle", ParentExecutionID: "root", Status: BGAgentRunning})
	calls := 0
	child := &BackgroundAgent{ID: "late", ParentExecutionID: "middle", Status: BGAgentRunning, cancel: func() { calls++ }}
	registry.Register("s", child)
	if child.GetStatus() != BGAgentCanceled || calls != 1 {
		t.Fatalf("late child status=%s, cancellations=%d", child.GetStatus(), calls)
	}
}

func TestBackgroundRegistrationRacingWithStopCannotLeaveLiveChildren(t *testing.T) {
	for i := 0; i < 100; i++ {
		registry := NewBackgroundAgentRegistry()
		registry.Register("s", &BackgroundAgent{ID: "parent", Status: BGAgentRunning})
		ctx, cancel := context.WithCancel(context.Background())
		child := &BackgroundAgent{ID: "child", ParentExecutionID: "parent", Status: BGAgentRunning, cancel: cancel}
		var workers sync.WaitGroup
		workers.Add(2)
		start := make(chan struct{})
		go func() { defer workers.Done(); <-start; registry.Register("s", child) }()
		go func() { defer workers.Done(); <-start; registry.CancelExecutionTree("s", "parent") }()
		close(start)
		workers.Wait()
		if child.GetStatus() != BGAgentCanceled || ctx.Err() != context.Canceled {
			t.Fatalf("iteration %d left a live child", i)
		}
	}
}
