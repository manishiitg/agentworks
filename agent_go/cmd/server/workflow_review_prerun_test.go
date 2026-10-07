package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PLAT-697 phase 0: the Workflow Review is a pre-run check. An unchanged,
// clean plan starts with no review; a changed plan is reviewed once, then
// runs; a break the review leaves stops the run, and the same plan state is
// never reviewed again.
func TestWorkflowReviewRunsOncePerPlanRevisionBeforeARun(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	ws := "Workflow/prerun-review"
	planning := filepath.Join(root, ws, "planning")
	if err := os.MkdirAll(planning, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(planning, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stepConfig := func(needsReview bool, reviewedAt string) string {
		flag := "false"
		if needsReview {
			flag = "true"
		}
		return `{"steps":[{"id":"step-a","agent_configs":{"drift_review":{"needs_review":` + flag + `,"reviewed_at":"` + reviewedAt + `","reviewed_by":"workflow-review","contract_version":3,"checks":[]}}}]}`
	}
	write("plan.json", `{"steps":[{"id":"step-a","type":"regular"}]}`)
	write("step_config.json", stepConfig(false, "2026-10-01T00:00:00Z"))

	reviews := 0
	fix := true
	original := workflowReviewSessionRunner
	t.Cleanup(func() { workflowReviewSessionRunner = original })
	workflowReviewSessionRunner = func(_ *SchedulerService, _ context.Context, _, _, reason string) workflowReviewOutcome {
		reviews++
		if !strings.Contains(reason, "step-a") {
			t.Errorf("review reason does not name the changed step: %q", reason)
		}
		if fix {
			write("step_config.json", stepConfig(false, "2026-10-07T12:00:00Z"))
		}
		return workflowReviewOutcome{completed: true, sessionID: "review-session"}
	}
	s := &SchedulerService{}
	ctx := context.Background()

	// Unchanged and clean: the run starts, no review.
	if gate := s.ensureWorkflowReviewedBeforeRun(ctx, ws, nil, "cron"); !gate.Proceed || gate.Reviewed || reviews != 0 {
		t.Fatalf("clean plan: gate=%+v reviews=%d, want proceed with no review", gate, reviews)
	}

	// Changed: reviewed once, then the run starts on the reviewed plan.
	write("step_config.json", stepConfig(true, "2026-10-01T00:00:00Z"))
	if gate := s.ensureWorkflowReviewedBeforeRun(ctx, ws, nil, "cron"); !gate.Proceed || !gate.Reviewed || reviews != 1 {
		t.Fatalf("changed plan: gate=%+v reviews=%d, want one review then proceed", gate, reviews)
	}
	if gate := s.ensureWorkflowReviewedBeforeRun(ctx, ws, nil, "cron"); !gate.Proceed || reviews != 1 {
		t.Fatalf("after the review: gate=%+v reviews=%d, want proceed without another review", gate, reviews)
	}
	if latest := LatestWorkflowReview(ws); latest == nil || latest.Outcome != workflowReviewOutcomeClean {
		t.Fatalf("latest review = %+v, want clean", latest)
	}

	// A break the review cannot fix stops the run, once, without a loop.
	fix = false
	write("step_config.json", stepConfig(true, "2026-10-07T12:00:00Z"))
	gate := s.ensureWorkflowReviewedBeforeRun(ctx, ws, nil, "cron")
	if gate.Proceed || reviews != 2 || !strings.Contains(gate.BlockReason, "step-a") {
		t.Fatalf("unfixable break: gate=%+v reviews=%d, want stopped with the reason after one review", gate, reviews)
	}
	if gate := s.ensureWorkflowReviewedBeforeRun(ctx, ws, nil, "cron"); gate.Proceed || reviews != 2 {
		t.Fatalf("same plan state: gate=%+v reviews=%d, want stopped again without a second review", gate, reviews)
	}
	// A run of another step is not held by step-a's break.
	if gate := s.ensureWorkflowReviewedBeforeRun(ctx, ws, []string{"step-b"}, "manual"); !gate.Proceed || reviews != 2 {
		t.Fatalf("unrelated step: gate=%+v reviews=%d, want proceed", gate, reviews)
	}
}
