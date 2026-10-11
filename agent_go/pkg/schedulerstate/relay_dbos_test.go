package schedulerstate

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRelayDBOSBindingSurvivesRestartAndBoundsRecovery(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "ledger.sqlite")
	s, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	binding := RelayDBOSBinding{RunID: "run", OwnerID: "owner", ReleaseHash: "v1", InputJSON: `{"a":1}`, VariablesJSON: `{}`, Deadline: time.Now().Add(time.Hour)}
	first, err := s.ReserveRelayDBOSAttempt(ctx, binding)
	if err != nil || first.Attempt != 1 {
		t.Fatalf("first attempt: %+v %v", first, err)
	}
	s.Close()
	s, err = Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, mutate := range []func(*RelayDBOSBinding){func(b *RelayDBOSBinding) { b.InputJSON = `{"a":2}` }, func(b *RelayDBOSBinding) { b.ReleaseHash = "v2" }, func(b *RelayDBOSBinding) { b.OwnerID = "new-owner" }, func(b *RelayDBOSBinding) { b.VariablesJSON = `{"x":1}` }} {
		changed := binding
		mutate(&changed)
		if _, err := s.ReserveRelayDBOSAttempt(ctx, changed); err == nil {
			t.Fatal("changed binding admitted")
		}
	}
	binding.Deadline = time.Now().Add(2 * time.Hour)
	for i := 2; i <= 3; i++ {
		got, err := s.ReserveRelayDBOSAttempt(ctx, binding)
		if err != nil || got.Attempt != i || !got.Deadline.Equal(first.Deadline) {
			t.Fatalf("recovered attempt: %+v %v", got, err)
		}
	}
	if _, err := s.ReserveRelayDBOSAttempt(ctx, binding); err == nil {
		t.Fatal("attempt budget reset after restart")
	}
	binding.RunID = "expired"
	binding.Deadline = time.Now().Add(-time.Minute)
	if _, err := s.ReserveRelayDBOSAttempt(ctx, binding); err == nil {
		t.Fatal("expired invocation admitted")
	}
}

func TestOnlyRestartInterruptedDBOSRunsCanBeClaimed(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []string{"dbos", "ordinary", "stopped", "failed"} {
		if err := s.BeginRun(ctx, Run{RunID: id, ScheduleID: "process", ScopeType: "workflow", ScopeID: "Workflow/test", LockKey: id}); err != nil {
			t.Fatal(err)
		}
		if id != "ordinary" {
			if _, err := s.ReserveRelayDBOSAttempt(ctx, RelayDBOSBinding{RunID: id, Deadline: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
		}
		state := StateInterrupted
		reason := "interrupted: server restarted"
		if id == "stopped" {
			state = StateStopped
		}
		if id == "failed" {
			state = StateFailed
		}
		if err := s.Transition(ctx, Transition{RunID: id, To: state, ErrorMessage: reason}); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := s.InterruptedDBOSRuns(ctx)
	if err != nil || len(runs) != 1 || runs[0].RunID != "dbos" {
		t.Fatalf("recovery candidates: %+v %v", runs, err)
	}
	for _, id := range []string{"ordinary", "stopped", "failed"} {
		if err := s.ResumeInterruptedDBOSRun(ctx, id, time.Now()); err == nil {
			t.Fatalf("reopened %s", id)
		}
	}
	if err := s.ResumeInterruptedDBOSRun(ctx, "dbos", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.ResumeInterruptedDBOSRun(ctx, "dbos", time.Now()); err == nil {
		t.Fatal("duplicate recovery claim succeeded")
	}
	run, _ := s.GetRun(ctx, "dbos")
	if run.State != StateStarting || run.CompletedAt != nil {
		t.Fatalf("bad recovered row: %+v", run)
	}
}
