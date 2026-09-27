package server

import (
	"context"
	"testing"
	"time"
)

// A new automation shows goal setup with Goal next; a written goal moves to
// Plan; a plan moves to Metrics. Checks read real state: the scaffold's TODO
// placeholder is not a goal, and an empty plan is not a plan.
func TestGoalSetupFollowsGoalThenPlanThenMetrics(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	stub, _ := newScheduleRunWorkspaceStub(t)
	ctx := context.Background()
	ws := "Workflow/new-automation"
	setFile := func(path, content string) {
		stub.mu.Lock()
		stub.files[ws+"/"+path] = content
		stub.mu.Unlock()
	}

	setFile("soul/soul.md", "# New\n\n## Objective\n<TODO: outcome bullets.>\n\n## Success Criteria\n<TODO: when it is done right.>\n")
	setFile("planning/plan.json", `{"steps":[]}`)
	status := buildWorkflowGoalSetupStatus(ctx, ws)
	if !status.Show || status.Next == nil || status.Next.ID != "goal" || status.Next.Command != "setup-goals" {
		t.Fatalf("a fresh automation must show setup with Goal next, got %+v", status)
	}

	setFile("soul/soul.md", "# New\n\n## Objective\n- Book 5 sales demos a week\n\n## Success Criteria\n- 5 demos booked in a calendar week\n")
	status = buildWorkflowGoalSetupStatus(ctx, ws)
	if status.Next == nil || status.Next.ID != "plan" || status.Next.Command != "design-plan" {
		t.Fatalf("with a goal written, Plan is next, got %+v", status)
	}

	setFile("planning/plan.json", `{"steps":[{"id":"find-leads","type":"message_sequence"}]}`)
	status = buildWorkflowGoalSetupStatus(ctx, ws)
	if status.Next == nil || status.Next.ID != "metrics" || status.Complete {
		t.Fatalf("with a goal and plan, Metrics is next, got %+v", status)
	}
}

// Goals are optional: a dismissed setup stays hidden, and so does setup for
// an automation that has already run (it is past its initial setup).
func TestGoalSetupHidesWhenDismissedOrAlreadyRunning(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	stub, _ := newScheduleRunWorkspaceStub(t)
	ctx := context.Background()

	dismissed := "Workflow/dismissed"
	stub.mu.Lock()
	stub.files[dismissed+"/"+workflowGoalSetupDismissFile] = `{"dismissed_at":"2026-09-27T00:00:00Z"}`
	stub.mu.Unlock()
	if status := buildWorkflowGoalSetupStatus(ctx, dismissed); status.Show || !status.Dismissed {
		t.Fatalf("a dismissed setup must stay hidden, got %+v", status)
	}

	running := "Workflow/running"
	if err := AppendScheduleRun(ctx, running, &ScheduleRunEntry{ID: "r1", ScheduleID: "daily", Status: "success", StartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if status := buildWorkflowGoalSetupStatus(ctx, running); status.Show || !status.HasRuns {
		t.Fatalf("an automation that has run is past initial setup, got %+v", status)
	}
}
