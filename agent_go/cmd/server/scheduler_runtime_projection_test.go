package server

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	"github.com/robfig/cron/v3"
)

func TestRuntimeProjectionClearsStaleRunningButPreservesOwnedPulse(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspace := &mockWorkspaceAPI{files: map[string]string{}}
	wsServer := httptest.NewServer(workspace)
	defer wsServer.Close()
	t.Setenv("WORKSPACE_API_URL", wsServer.URL)
	svc := NewSchedulerService(nil)
	ws, id := "Workflow/projection", "schedule-1"
	start := time.Now().UTC().Add(-time.Hour)
	if err := WriteScheduleRuns(context.Background(), ws, []ScheduleRunEntry{{ID: "run-1", ScheduleID: id, SessionID: "session-1", StartedAt: start, Status: "success"}}); err != nil {
		t.Fatal(err)
	}
	svc.runtimeStates[workflowScheduleRuntimeKey(ws, id)] = &ScheduleRuntimeState{ActiveRunID: "run-1", LastStatus: "running", LastSessionID: "session-1", LastRunAt: &start, LastError: "stale error"}
	svc.registerScheduleRunContext("run-1")
	if got := svc.GetRuntimeStateForWorkflow(ws, id); got.LastStatus != "running" {
		t.Fatalf("active Autopilot overwritten: %+v", got)
	}
	svc.releaseScheduleRunContext("run-1")
	if got := svc.GetRuntimeStateForWorkflow(ws, id); got.LastStatus != "success" || got.ActiveRunID != "" || got.LastError != "" {
		t.Fatalf("stale state retained: %+v", got)
	}
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "scheduler.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc.stateStore = store
	if err := store.BeginRun(context.Background(), schedulerstate.Run{RunID: "run-1", ScopeType: "workflow", ScopeID: ws, LockKey: ws, ScheduleID: id, StartedAt: start}); err != nil {
		t.Fatal(err)
	}
	if err := store.ForceTerminal(context.Background(), schedulerstate.Transition{RunID: "run-1", To: schedulerstate.StatePartial, ErrorMessage: "Autopilot incomplete"}); err != nil {
		t.Fatal(err)
	}
	if got := svc.GetRuntimeStateForWorkflow(ws, id); got.LastStatus != "partial" || got.LastError != "Autopilot incomplete" {
		t.Fatalf("lost durable Autopilot outcome: %+v", got)
	}
}

func TestRuntimeProjectionDoesNotUseSessionAsRunIdentity(t *testing.T) {
	now := time.Now().UTC()
	old := now.Add(-time.Hour)
	state := ScheduleRuntimeState{LastStatus: "running", LastSessionID: "reused", LastRunAt: &old}
	runs := []ScheduleRunEntry{{ID: "old", ScheduleID: "s", SessionID: "reused", StartedAt: old, Status: "error"}, {ID: "new", ScheduleID: "s", SessionID: "reused", StartedAt: now, Status: "success"}}
	got := mergeRuntimeStateWithRuns(state, "s", runs, false, "")
	if got.LastStatus != "success" || !got.LastRunAt.Equal(now) {
		t.Fatalf("latest run not selected: %+v", got)
	}
	state.LastStatus = "partial"
	state.LastRunAt = &now
	if got := mergeRuntimeStateWithRuns(state, "s", runs, false, ""); got.LastStatus != "partial" {
		t.Fatal("workflow-only history overwrote terminal Autopilot outcome")
	}
}

func TestRuntimeProjectionDurableTerminalOverridesSameRunSuccess(t *testing.T) {
	start := time.Now().UTC()
	state := ScheduleRuntimeState{LastStatus: "success", LastRunAt: &start, LastSessionID: "session"}
	runs := []ScheduleRunEntry{{ID: "run", ScheduleID: "s", SessionID: "session", StartedAt: start, Status: "partial", Error: "Autopilot failed"}}
	got := mergeRuntimeStateWithRuns(state, "s", runs, false, "run")
	if got.LastStatus != "partial" || got.LastError != "Autopilot failed" {
		t.Fatalf("lost durable result: %+v", got)
	}
	// Workflow-only history cannot contradict a final whole-job result.
	got = mergeRuntimeStateWithRuns(state, "s", runs, false, "")
	if got.LastStatus != "success" || got.LastError != "" {
		t.Fatalf("mixed unrelated status/error: %+v", got)
	}
}

func TestNextWorkflowScheduleRunUsesCurrentTimeAndScope(t *testing.T) {
	svc := NewSchedulerService(nil)
	now := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	schedule, err := cron.ParseStandard("0 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	sctx := &ScheduleContext{WorkspacePath: "Workflow/a", Schedule: WorkflowSchedule{ID: "s", Enabled: true}}
	svc.jobs["cron"] = &registeredJob{sctx: sctx, cronSched: schedule}
	afterPulse := now.Add(2*time.Hour + 30*time.Minute)
	got := svc.nextWorkflowScheduleRunAt("Workflow/a", "s", afterPulse)
	want := now.Add(3 * time.Hour)
	if got == nil || !got.Equal(want) {
		t.Fatalf("next=%v want=%v", got, want)
	}
	if got := svc.nextWorkflowScheduleRunAt("Workflow/b", "s", afterPulse); got != nil {
		t.Fatal("cross-workflow schedule leaked")
	}
	sctx.Schedule.Enabled = false
	if got := svc.nextWorkflowScheduleRunAt("Workflow/a", "s", afterPulse); got != nil {
		t.Fatal("paused schedule has next run")
	}
	sctx.Schedule.Enabled = true
	delete(svc.jobs, "cron")
	past, future, later := now.Add(-time.Hour), now.Add(time.Hour), now.Add(2*time.Hour)
	for key, at := range map[string]*time.Time{"past": &past, "future": &future, "later": &later} {
		svc.jobs[key] = &registeredJob{sctx: sctx, runAt: at}
	}
	if got := svc.nextWorkflowScheduleRunAt("Workflow/a", "s", now); got == nil || !got.Equal(future) {
		t.Fatalf("calendar next=%v", got)
	}
	if got := svc.nextWorkflowScheduleRunAt("Workflow/a", "s", later); got != nil {
		t.Fatalf("exhausted calendar next=%v", got)
	}
}
