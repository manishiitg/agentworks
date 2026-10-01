package schedulerstate

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCapacityWaitSurvivesRestartAndClaimsResumeOnce(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "runs.sqlite")
	s, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	run := Run{RunID: "published", ScopeType: "workflow", ScopeID: "Workflow/.relay_releases/hash/v1", LockKey: "invocation", ScheduleID: "function", TriggerSource: "webhook", RunFolder: "original-folder"}
	if err := s.BeginRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	for _, state := range []State{StateWorkflowRunning, StateWorkflowFinished, StateWaitingForCapacity} {
		if err := s.Transition(ctx, Transition{RunID: run.RunID, To: state}); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s, err = Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, err := s.InterruptActiveRuns(ctx, "server restarted", time.Now()); err != nil || n != 0 {
		t.Fatalf("waiting interrupted: %d %v", n, err)
	}
	waits, err := s.WaitingCapacityRuns(ctx)
	if err != nil || len(waits) != 1 || waits[0].RunFolder != run.RunFolder {
		t.Fatalf("durable waits: %+v %v", waits, err)
	}
	var claims atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.ResumeCapacityRun(ctx, run.RunID, time.Now()) == nil {
				claims.Add(1)
			}
		}()
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatalf("resume claimed %d times", claims.Load())
	}
	events, err := s.ListEvents(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	resumes := 0
	for _, event := range events {
		if event.FromState == StateWaitingForCapacity && event.ToState == StateStarting {
			resumes++
		}
	}
	if resumes != 1 {
		t.Fatalf("resume events = %d", resumes)
	}
	for _, state := range []State{StateWorkflowRunning, StateWorkflowFinished, StateCompleted} {
		if err := s.Transition(ctx, Transition{RunID: run.RunID, To: state}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetRun(ctx, run.RunID)
	if err != nil || got.State != StateCompleted || got.RunFolder != run.RunFolder {
		t.Fatalf("resumed identity: %+v %v", got, err)
	}
}
