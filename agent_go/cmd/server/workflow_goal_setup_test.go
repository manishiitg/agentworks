package server

import (
	"context"
	"testing"
	"time"
)

// A new automation shows goal setup with Goal next; a written goal moves to
// Plan; a plan moves to Metrics; the Dashboard comes last. Checks read real state: the scaffold's TODO
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
	status := buildWorkflowGoalSetupStatus(ctx, ws, nil)
	if !status.Show || status.Next == nil || status.Next.ID != "goal" || status.Next.Command != "setup-goals" {
		t.Fatalf("a fresh automation must show setup with Goal next, got %+v", status)
	}

	setFile("soul/soul.md", "# New\n\n## Objective\n- Book 5 sales demos a week\n\n## Success Criteria\n- 5 demos booked in a calendar week\n")
	status = buildWorkflowGoalSetupStatus(ctx, ws, nil)
	if status.Next == nil || status.Next.ID != "plan" || status.Next.Command != "design-plan" {
		t.Fatalf("with a goal written, Plan is next, got %+v", status)
	}

	setFile("planning/plan.json", `{"steps":[{"id":"find-leads","type":"message_sequence"}]}`)
	status = buildWorkflowGoalSetupStatus(ctx, ws, nil)
	if status.Next == nil || status.Next.ID != "metrics" || status.Complete {
		t.Fatalf("with a goal and plan, Metrics is next, got %+v", status)
	}
	last := status.Checks[len(status.Checks)-1]
	if last.ID != "dashboard" || last.Done || last.Command != "design-dashboard" {
		t.Fatalf("the dashboard is the last check and pending, got %+v", last)
	}
	setFile("db/reports/index.html", "<html><body>Demos booked this week</body></html>")
	for _, check := range buildWorkflowGoalSetupStatus(ctx, ws, nil).Checks {
		if check.ID == "dashboard" && !check.Done {
			t.Fatal("a designed dashboard must count as done")
		}
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
	if status := buildWorkflowGoalSetupStatus(ctx, dismissed, nil); status.Show || !status.Dismissed {
		t.Fatalf("a dismissed setup must stay hidden, got %+v", status)
	}

	running := "Workflow/running"
	if err := AppendScheduleRun(ctx, running, &ScheduleRunEntry{ID: "r1", ScheduleID: "daily", Status: "success", StartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if status := buildWorkflowGoalSetupStatus(ctx, running, nil); status.Show || !status.HasRuns {
		t.Fatalf("an automation that has run is past initial setup, got %+v", status)
	}
}

// A playbook is an optional first step: it never becomes "next" or blocks
// completion, and an installed one is reported for the setup chats.
func TestGoalSetupPlaybookIsOptionalAndReported(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	_, _ = newScheduleRunWorkspaceStub(t)
	ctx := context.Background()
	status := buildWorkflowGoalSetupStatus(ctx, "Workflow/p", &WorkflowManifest{})
	if status.Checks[0].ID != "playbook" || !status.Checks[0].Optional || status.Checks[0].Done {
		t.Fatalf("playbook is the optional first check, got %+v", status.Checks[0])
	}
	if status.Next == nil || status.Next.ID != "goal" {
		t.Fatalf("an optional playbook must not be next, got %+v", status.Next)
	}
	with := buildWorkflowGoalSetupStatus(ctx, "Workflow/p", &WorkflowManifest{InstalledPlaybooks: []InstalledPlaybook{{ID: "website-growth-loop", Title: "Website Growth Loop", SkillName: "agentworks-playbook-website-growth-loop", Status: "draft"}}})
	if !with.Checks[0].Done || len(with.Playbooks) != 1 || with.Playbooks[0].SkillName != "agentworks-playbook-website-growth-loop" {
		t.Fatalf("an installed playbook must be done and reported, got %+v", with)
	}
}

// Pulse is the last setup step: pending until it is on, never ahead of the
// steps before it, and absent for a Relay (which has no Pulse).
func TestGoalSetupEndsWithPulse(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	stub, _ := newScheduleRunWorkspaceStub(t)
	ctx := context.Background()
	ws := "Workflow/owned-goal"
	stub.mu.Lock()
	stub.files[ws+"/soul/soul.md"] = "# G\n\n## Objective\n- Book demos\n\n## Success Criteria\n- 5 a week\n"
	stub.files[ws+"/planning/plan.json"] = `{"steps":[{"id":"a","type":"message_sequence"}]}`
	stub.files[ws+"/db/reports/index.html"] = "<html>demos</html>"
	stub.mu.Unlock()

	off := buildWorkflowGoalSetupStatus(ctx, ws, &WorkflowManifest{})
	last := off.Checks[len(off.Checks)-1]
	if last.ID != "pulse" || last.Done || last.Command != "" {
		t.Fatalf("Pulse is the last, pending, chat-free check, got %+v", last)
	}
	if off.Complete || off.Next == nil || off.Next.ID == "pulse" {
		t.Fatalf("Pulse must not come before the steps it follows (metrics are not set): %+v", off)
	}

	on := buildWorkflowGoalSetupStatus(ctx, ws, &WorkflowManifest{Pulse: &WorkflowPulseConfig{Enabled: true}})
	if !on.Checks[len(on.Checks)-1].Done {
		t.Fatalf("an enabled Pulse counts as done: %+v", on.Checks)
	}

	relay := buildWorkflowGoalSetupStatus(ctx, ws, &WorkflowManifest{Kind: "relay"})
	for _, check := range relay.Checks {
		if check.ID == "pulse" {
			t.Fatal("a Relay has no Pulse step")
		}
	}
}
