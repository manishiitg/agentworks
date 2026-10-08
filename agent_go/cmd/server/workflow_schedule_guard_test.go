package server

import (
	"context"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScheduleCollisionGuardLiveForceAndRestart(t *testing.T) {
	s, err := schedulerstate.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	api := &StreamingAPI{scheduler: &SchedulerService{stateStore: s}}
	ctx := context.Background()
	check := api.scheduleCollisionCheck("Workflow/demo", "builder-1", "interactive")
	if err := check(ctx, "execute_step", nil); err != nil {
		t.Fatal(err)
	}
	r := schedulerstate.Run{RunID: "r1", ScopeType: "workflow", ScopeID: "Workflow/demo", LockKey: "workflow:demo", ScheduleID: "daily"}
	if err := s.BeginRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := check(ctx, "execute_step", nil); err == nil || !strings.Contains(err.Error(), "schedule_running") || !strings.Contains(err.Error(), "Stop the current workflow-mutation batch") {
		t.Fatalf("missing warning: %v", err)
	}
	if err := check(ctx, "execute_step", map[string]interface{}{"force": "true"}); err == nil {
		t.Fatal("accepted string force")
	}
	if err := check(ctx, "execute_step", map[string]interface{}{"force": true}); err != nil {
		t.Fatal(err)
	}
	if run, err := s.ActiveRunForScope(ctx, "workflow", r.ScopeID); err != nil || run == nil {
		t.Fatal("force removed lease")
	}
	if api.scheduleCollisionCheck("Workflow/demo", "schedule-cron-1", "cron") != nil {
		t.Fatal("schedule blocks itself")
	}
	if err := api.scheduleCollisionCheck("Workflow/other", "builder-2", "")(ctx, "execute_step", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InterruptActiveRuns(ctx, "restart", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := check(ctx, "execute_step", nil); err != nil {
		t.Fatalf("stale run blocked builder: %v", err)
	}
}

// A Pulse turn only reads: the runs it asks the Builder for must not be refused
// because of it (owner, 2026-10-08; linkedin and social-media re-runs were
// blocked by the Pulse turn's own lease). A real schedule still blocks.
func TestScheduleCollisionGuardIgnoresPulseTurns(t *testing.T) {
	s, err := schedulerstate.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	api := &StreamingAPI{scheduler: &SchedulerService{stateStore: s}}
	ctx := context.Background()
	check := api.scheduleCollisionCheck("Workflow/demo", "builder-1", "interactive")
	pulse := schedulerstate.Run{RunID: "p1", ScopeType: "workflow", ScopeID: "Workflow/demo", LockKey: "workflow-pulse:demo", ScheduleID: manualWorkflowPulseScheduleID}
	if err := s.BeginRun(ctx, pulse); err != nil {
		t.Fatal(err)
	}
	if err := check(ctx, "execute_step", nil); err != nil {
		t.Fatalf("a Pulse turn blocked the Builder's run: %v", err)
	}
	daily := schedulerstate.Run{RunID: "r1", ScopeType: "workflow", ScopeID: "Workflow/demo", LockKey: "workflow:demo", ScheduleID: "daily"}
	if err := s.BeginRun(ctx, daily); err != nil {
		t.Fatal(err)
	}
	if err := check(ctx, "execute_step", nil); err == nil || !strings.Contains(err.Error(), "schedule_running") {
		t.Fatalf("a real schedule must still block: %v", err)
	}
}
