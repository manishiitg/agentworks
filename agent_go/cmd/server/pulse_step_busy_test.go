package server

import (
	"context"
	"strings"
	"testing"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Pulse does not re-run a step the workflow's own run is executing right now;
// its own runs, and other Pulse runs, do not count.
func TestPulseHoldsAStepTheWorkflowIsRunning(t *testing.T) {
	const ws, step = "Workflow/salesoutreach", "step-send-email-outreach"
	pulse := "schedule-cron--pulse-fi_1790561999487679000"
	check := (&StreamingAPI{}).pulseStepBusyCheck(pulse)

	if busy := check(context.Background(), ws, step); busy != "" {
		t.Fatalf("nothing running yet, got %q", busy)
	}
	releaseOwn := stepworkflow.RegisterRunningWorkflowStep(ws, step, "exec-own", pulse)
	if busy := check(context.Background(), ws, step); busy != "" {
		t.Fatalf("Pulse's own run of the step must not block it, got %q", busy)
	}
	releaseOwn()

	release := stepworkflow.RegisterRunningWorkflowStep(ws, step, "exec-sched", "schedule-cron--688df853_1790500000000000000")
	busy := check(context.Background(), ws, step)
	if !strings.Contains(busy, "688df853") {
		t.Fatalf("the workflow's scheduled run of the step must hold Pulse, got %q", busy)
	}
	if other := check(context.Background(), ws, "step-check-email-replies"); other != "" {
		t.Fatalf("another step is not busy, got %q", other)
	}
	release()
	if busy := check(context.Background(), ws, step); busy != "" {
		t.Fatalf("after the run finishes the step is free, got %q", busy)
	}
}

func TestIsPulseScheduleSessionID(t *testing.T) {
	for sid, want := range map[string]bool{
		"schedule-cron--pulse-fi_1":   true,
		"schedule-manual--manual-p_2": true,
		"schedule-cron--688df853_3":   false,
		"chat-abc":                    false,
	} {
		if got := isPulseScheduleSessionID(sid); got != want {
			t.Errorf("%s: got %v want %v", sid, got, want)
		}
	}
}
