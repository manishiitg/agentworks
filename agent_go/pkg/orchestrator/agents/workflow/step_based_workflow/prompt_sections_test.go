package step_based_workflow

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDependencyPathCandidatesRepairsProducerIDDependency(t *testing.T) {
	steps := []PlanStepInterface{
		&ScriptedPlanStep{Type: StepTypeScripted, CommonStepFields: CommonStepFields{
			ID: "step-engagement-performance", ContextOutput: FlexibleContextOutput("performance_checked.json"),
		}},
		&ScriptedPlanStep{Type: StepTypeScripted, CommonStepFields: CommonStepFields{
			ID: "step-performance-analytics", ContextDependencies: []string{"step-engagement-performance"},
		}},
	}
	candidates := ResolveDependencyPathCandidates(
		"step-engagement-performance", 1, "step-2", steps,
		"Workflow/linkedin/runs/iteration-0/execution", "/workspace-docs", nil,
	)
	want := filepath.Join("/workspace-docs", "Workflow/linkedin/runs/iteration-0/execution", "step-engagement-performance", "performance_checked.json")
	if len(candidates) == 0 || candidates[0] != want {
		t.Fatalf("producer-ID dependency candidates = %#v, want first candidate %q", candidates, want)
	}
}

// TestManagedDBGuidanceNeverRequiresDBReadmeUnconditionally reproduces
// Upwork PUL-EDFF0710: the injected boilerplate told every step to read
// db/README.md, but a step's actual Folder Guard read grant does not always
// include it (e.g. background/KB-harvest-style steps, unlike ordinary
// execution steps via setupExecutionFolderGuard). An agent that follows an
// instruction it has no way to satisfy burns a call learning that from a
// denial instead of being told the always-available fallback up front. The
// guidance must offer query_workflow_db describe as a fallback rather than
// asserting db/README.md is simply readable.
func TestManagedDBGuidanceNeverRequiresDBReadmeUnconditionally(t *testing.T) {
	for _, access := range []string{DBAccessRead, DBAccessReadWrite} {
		guidance := BuildManagedWorkflowDBGuidance(access)
		if strings.Contains(guidance, "Read `db/README.md` before relying") {
			t.Fatalf("access=%q guidance still asserts db/README.md is unconditionally readable:\n%s", access, guidance)
		}
		if !strings.Contains(guidance, "not every session's Folder Guard grants it") {
			t.Fatalf("access=%q guidance dropped the Folder Guard caveat entirely:\n%s", access, guidance)
		}
		if !strings.Contains(guidance, `query_workflow_db`) || !strings.Contains(guidance, `action: "describe"`) {
			t.Fatalf("access=%q guidance missing the always-available query_workflow_db describe fallback:\n%s", access, guidance)
		}
	}
}

func TestToAbsPathsLeavesAbsoluteHostPathsUntouched(t *testing.T) {
	got := toAbsPaths("/app/workspace-docs", []string{
		"Workflow/demo/execution",
		"/Users/mipl/Downloads",
	})

	if got[0] != "/app/workspace-docs/Workflow/demo/execution" {
		t.Fatalf("expected workspace-relative path to be rooted, got %q", got[0])
	}
	if got[1] != "/Users/mipl/Downloads" {
		t.Fatalf("expected absolute host path to stay untouched, got %q", got[1])
	}
}
