package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestManualPulseScheduleContract(t *testing.T) {
	if manualWorkflowPulseScheduleID == "" {
		t.Fatal("manual workflow Pulse schedule id must not be empty")
	}

	manifest := &WorkflowManifest{ID: "workflow-id", Label: "Workflow", Pulse: &WorkflowPulseConfig{Enabled: true}}
	sched := WorkflowSchedule{
		ID:           manualWorkflowPulseScheduleID,
		Name:         "Run Pulse",
		Mode:         "workshop",
		WorkshopMode: "run",
	}
	sctx := buildScheduleContext("/tmp/workflow", manifest, sched)
	sctx.TriggerSource = "manual"
	sctx.ForcePulseReview = true
	sctx.PulseOnly = true
	sctx.PulseEvidenceRunFolder = "iteration-2/default"
	sctx.PulseEvidenceRunStatus = "error"

	if sctx.Schedule.Mode != "workshop" || sctx.Schedule.WorkshopMode != "run" {
		t.Fatalf("manual Pulse must use the workshop preflight path: %+v", sctx.Schedule)
	}
	if !sctx.ForcePulseReview {
		t.Fatal("manual Pulse must force Pulse without changing recurring schedules")
	}
	if !sctx.PulseOnly {
		t.Fatal("manual toolbar action must be marked Pulse-only")
	}
	if messages := scheduledWorkshopMessages(sctx); len(messages) != 0 {
		t.Fatalf("Pulse-only action must not enqueue a workflow message: %v", messages)
	}
	turns, err := manualWorkflowUpgradeTurns(manifest, scheduledWorkshopMessages(sctx), sctx.WorkspacePath)
	if err != nil {
		t.Fatalf("build Pulse preflight turns: %v", err)
	}
	if len(turns) == 0 {
		t.Fatal("an unversioned workflow must still receive version preflight turns")
	}
	for _, turn := range turns {
		if turn.upgradeTarget == "" || strings.HasPrefix(turn.label, "schedule-message-") {
			t.Fatalf("Pulse-only action queued a non-upgrade turn: %+v", turn)
		}
	}
	if !shouldRunPulseLifecycle(sctx, manifest) {
		t.Fatal("one-off Pulse must execute without a recurring Pulse schedule")
	}

	normalCtx := buildScheduleContext("/tmp/workflow", manifest, WorkflowSchedule{Mode: "workshop", WorkshopMode: "run", AfterRun: &ScheduleAfterRun{Backup: true}})
	normalMessages := scheduledWorkshopMessages(normalCtx)
	if len(normalMessages) != 1 || !strings.Contains(normalMessages[0], "run_full_workflow") {
		t.Fatalf("ordinary empty schedule must retain the default workflow message: %v", normalMessages)
	}

	manifest.Pulse.Enabled = false
	if shouldRunPulseLifecycle(sctx, manifest) {
		t.Fatal("forced/manual Pulse bypassed OFF")
	}
	sctx.ForcePulseReview = false
	if shouldRunPulseLifecycle(sctx, manifest) {
		t.Fatal("ordinary run must not run Pulse when pulse.enabled is false")
	}
	manifest.Pulse = &WorkflowPulseConfig{Enabled: true}
	if !shouldRunPulseLifecycle(normalCtx, manifest) {
		t.Fatal("ordinary scheduled run must run Pulse when pulse.enabled is true")
	}
}

// TestPulseReviewOnlyScheduleContractMirrorsManualPulseTrigger retains the
// legacy schedule parser until startup migration removes old entries.
func TestPulseReviewOnlyScheduleContractMirrorsManualPulseTrigger(t *testing.T) {
	manifest := &WorkflowManifest{ID: "workflow-id", Label: "Workflow", Pulse: &WorkflowPulseConfig{Enabled: true}}
	sched := WorkflowSchedule{
		ID:              "periodic-pulse-review",
		Name:            "Periodic Pulse Review",
		Mode:            "workshop",
		WorkshopMode:    "run",
		CronExpression:  "0 3 * * *",
		PulseReviewOnly: true,
	}
	sctx := buildScheduleContext("/tmp/workflow", manifest, sched)

	if !sctx.PulseOnly {
		t.Fatal("a PulseReviewOnly schedule must set sctx.PulseOnly")
	}
	if !sctx.ForcePulseReview {
		t.Fatal("a PulseReviewOnly schedule must force Pulse to run")
	}
	if sctx.PulseEvidenceRunFolder != "" {
		t.Fatalf("PulseEvidenceRunFolder = %q, want empty — a periodic pass must not be pinned to a single folder", sctx.PulseEvidenceRunFolder)
	}
	if !shouldRunPulseLifecycle(sctx, manifest) {
		t.Fatal("a PulseReviewOnly schedule must execute Pulse")
	}
	if messages := scheduledWorkshopMessages(sctx); len(messages) != 0 {
		t.Fatalf("PulseReviewOnly must not enqueue a workflow message: %v", messages)
	}

	// A schedule with the flag unset (the overwhelming majority of schedules,
	// including every existing one before this field existed) must be
	// completely unaffected.
	ordinary := buildScheduleContext("/tmp/workflow", manifest, WorkflowSchedule{Mode: "workshop", WorkshopMode: "run"})
	if ordinary.PulseOnly || ordinary.ForcePulseReview {
		t.Fatalf("an ordinary schedule must not be marked Pulse-only: %+v", ordinary)
	}
	manifest.Schedules = []WorkflowSchedule{{
		ID: "periodic-pulse-review", Enabled: true, PulseReviewOnly: true,
	}}
	if !shouldRunPulseLifecycle(ordinary, manifest) {
		t.Fatal("a legacy Pulse schedule must keep post-run Pulse enabled during migration")
	}
}

func TestLatestRetainedPulseEvidenceSkipsPriorManualPulseRuns(t *testing.T) {
	now := time.Now().UTC()
	runs := []ScheduleRunEntry{
		{ID: "pulse", ScheduleID: manualWorkflowPulseScheduleID, RunFolder: "iteration-0", Status: "success", StartedAt: now},
		{ID: "workflow", ScheduleID: "daily", RunFolder: "iteration-1/group-a", Status: "error", StartedAt: now.Add(-time.Minute)},
	}
	runFolder, status, ok := latestRetainedPulseEvidenceFromRuns(runs)
	if !ok || runFolder != "iteration-1/group-a" || status != "error" {
		t.Fatalf("latest retained evidence = (%q, %q), want (%q, %q)", runFolder, status, "iteration-1/group-a", "error")
	}
}

func TestLatestRetainedPulseEvidenceFallsBackBeforeFirstRun(t *testing.T) {
	runFolder, status := latestRetainedPulseEvidence(context.Background(), t.TempDir())
	if runFolder != "iteration-0" || status != "unknown" {
		t.Fatalf("fallback evidence = (%q, %q), want (iteration-0, unknown)", runFolder, status)
	}
}

// Pulse reads metrics/history through its own tools and executes no workflow.
// A failed execution-folder service cannot fail a report before its turn starts.
func TestPulseOnlyDoesNotReadExecutionFolders(t *testing.T) {
	var requests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer remote.Close()
	t.Setenv("WORKSPACE_API_URL", remote.URL)
	s := &SchedulerService{}
	session, folder, err := s.executeJob(context.Background(), &ScheduleContext{PulseOnly: true, WorkspacePath: "Workflow/example", Schedule: WorkflowSchedule{ID: manualWorkflowPulseScheduleID}}, "check-1")
	if err != nil || session == "" || folder != "" || requests.Load() != 0 {
		t.Fatalf("Pulse-only used execution preflight: session=%s folder=%s requests=%d err=%v", session, folder, requests.Load(), err)
	}
}
