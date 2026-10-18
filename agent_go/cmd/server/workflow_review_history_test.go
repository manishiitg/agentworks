package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A prior failed/stale AI review must never prevent a requested run. Drive the
// registered tool boundary with real saved review/config files; keep the normal
// contract/graph guards, and preserve the old review as history.
func TestSavedWorkflowReviewNeverHoldsExecution(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	ws := serveContractGuardManifest(t, WorkflowContractCurrentVersion)
	planning := filepath.Join(root, ws, "planning")
	if err := os.MkdirAll(planning, 0755); err != nil {
		t.Fatal(err)
	}
	write := func(name, raw string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(planning, name), []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("plan.json", `{"steps":[{"id":"step-a","type":"scripted","description":"Produce a result."}]}`)
	write("step_config.json", `{"steps":[{"id":"step-a","agent_configs":{"drift_review":{"needs_review":true,"contract_version":3,"checks":[]}}}]}`)
	draft := &productSurfaceDraft{}
	guard := workflowContractExecutionGuardRegistrar{definitionRegistrar: draft, workspacePath: ws}
	started := 0
	for _, name := range []string{"execute_step", "run_full_workflow"} {
		if err := guard.RegisterCustomTool(name, "Run", map[string]interface{}{"type": "object"}, func(context.Context, map[string]interface{}) (string, error) {
			started++
			return "execution started", nil
		}, "workflow"); err != nil {
			t.Fatal(err)
		}
	}
	for _, outcome := range []string{"unresolved", "incomplete", "running"} {
		write("workflow_review.json", `{"version":1,"reviewed_revisions":["unchanged-plan"],"latest":{"revision":"unchanged-plan","outcome":"`+outcome+`","unresolved":[{"step_id":"step-a","reason":"The plan changed or a prior drift check remains unresolved"}]}}`)
		for _, name := range []string{"execute_step", "run_full_workflow"} {
			out, err := draft.tools[name].exec(context.Background(), map[string]interface{}{"step_id": "step-a"})
			if err != nil || !strings.Contains(out, "execution started") {
				t.Fatalf("%s held by %s review: %q %v", name, outcome, out, err)
			}
		}
		if latest := LatestWorkflowReview(ws); latest == nil || latest.Outcome != outcome {
			t.Fatalf("old %s review history was changed: %+v", outcome, latest)
		}
	}
	if started != 6 {
		t.Fatalf("started %d runs, want 6", started)
	}
}
