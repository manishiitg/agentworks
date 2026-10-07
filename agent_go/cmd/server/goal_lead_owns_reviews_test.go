package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// PLAT-697 (owner, 2026-10-07): for a workflow with a goal, QA and
// Architecture belong to its Pulse conversation. The full pass runs Goal Work
// only (in that conversation), with no Architecture or Technical turn; a
// workflow without a goal keeps the old order. The safety net stays: a failed
// run wakes the conversation for exactly one turn, however often the launcher
// looks.
func TestGoalWorkflowPassHasNoReviewTurnsAndAFailedRunWakesPulseOnce(t *testing.T) {
	allDue := func(string) bool { return true }
	labels := func(steps []pulseLifecycleStep) string {
		out := []string{}
		for _, step := range steps {
			out = append(out, fmt.Sprintf("%s(lead=%v)", step.label, step.goalLead))
		}
		return strings.Join(out, ",")
	}
	if got := labels(pulsePassModuleSteps("run-1", true, allDue)); got != "strategic-review(lead=true)" {
		t.Fatalf("goal workflow pass = %s, want Goal Work only, in the Pulse conversation", got)
	}
	if got := labels(pulsePassModuleSteps("run-1", false, allDue)); got != "strategic-review(lead=false),architecture-review(lead=false),technical-review(lead=false)" {
		t.Fatalf("workflow without a goal = %s, want the unchanged order", got)
	}

	env := newCrewFunctionEnv(t)
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	const ws = "Workflow/reports"
	if err := os.MkdirAll(filepath.Join(root, "Workflow", "reports"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	finished := now.Add(-30 * time.Minute)
	runs := []ScheduleRunEntry{
		{ID: "pulse-1", ScheduleID: manualWorkflowPulseScheduleID, Status: "error", StartedAt: now.Add(-20 * time.Minute)},
		{ID: "run-ok", ScheduleID: "daily", Status: "success", StartedAt: now.Add(-3 * time.Hour)},
		{ID: "run-failed", ScheduleID: "daily", Status: "error", Error: "step publish: API returned 401", RunFolder: "iteration-7", StartedAt: now.Add(-time.Hour), CompletedAt: &finished},
	}
	if err := WriteScheduleRuns(ctx, ws, runs); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var queries []string
	previousRunner, previousStart := goalLeadTurnRunner, startGoalLeadBackgroundTurn
	goalLeadTurnRunner = func(_ context.Context, reqMap map[string]interface{}, _, _ string) (internalSessionTurnResult, error) {
		mu.Lock()
		defer mu.Unlock()
		queries = append(queries, fmt.Sprint(reqMap["query"]))
		return internalSessionTurnResult{FinalResponse: "The publish step failed on a 401; asked for a QA run."}, nil
	}
	startGoalLeadBackgroundTurn = func(run func()) { run() }
	t.Cleanup(func() { goalLeadTurnRunner, startGoalLeadBackgroundTurn = previousRunner, previousStart })

	for tick := 0; tick < 3; tick++ {
		env.api.scheduler.wakeGoalLeadOnRunFailures(ctx, ws, now.Add(time.Duration(tick)*time.Minute))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(queries) != 1 {
		t.Fatalf("Pulse turns = %d, want exactly one for the one failed run", len(queries))
	}
	for _, want := range []string{"a run of this workflow failed", "run run-failed", "step publish: API returned 401", "record_pulse_qa_request"} {
		if !strings.Contains(queries[0], want) {
			t.Fatalf("run-failure turn is missing %q:\n%s", want, queries[0])
		}
	}
	if strings.Contains(queries[0], "pulse-1") {
		t.Fatalf("a Pulse pass's own failure is not a workflow run failure:\n%s", queries[0])
	}
}
