package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A step whose input nothing produces is refused before it starts in enforce
// mode, reported in warn mode (the default) and ignored when off. A step with
// readable inputs is never touched (PLAT-579).
func TestWorkflowGraphPreflightModes(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	planning := filepath.Join(docs, "Workflow", "g", "planning")
	if err := os.MkdirAll(planning, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := `{"steps":[
	 {"type":"regular","id":"step-a","description":"A.","context_output":"a.json"},
	 {"type":"regular","id":"step-b","description":"B.","context_dependencies":["a.json","ghost.json"],"context_output":"b.json"}]}`
	if err := os.WriteFile(filepath.Join(planning, "plan.json"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AGENTWORKS_GRAPH_STRICT", "")
	notice, err := workflowGraphPreflight("Workflow/g", "step-b")
	if err != nil || !strings.Contains(notice, "ghost.json") {
		t.Fatalf("warn mode must report and not refuse: %q, %v", notice, err)
	}
	if notice, err := workflowGraphPreflight("Workflow/g", "step-a"); notice != "" || err != nil {
		t.Fatalf("a step with readable inputs must pass: %q, %v", notice, err)
	}

	t.Setenv("AGENTWORKS_GRAPH_STRICT", "enforce")
	if _, err := workflowGraphPreflight("Workflow/g", "step-b"); err == nil || !strings.Contains(err.Error(), "workflow_graph_check_failed") || !strings.Contains(err.Error(), "ghost.json") {
		t.Fatalf("enforce mode must refuse the step: %v", err)
	}
	if _, err := workflowGraphPreflight("Workflow/g", ""); err == nil {
		t.Fatal("enforce mode must refuse a whole run that always reaches the broken step")
	}

	t.Setenv("AGENTWORKS_GRAPH_STRICT", "off")
	if notice, err := workflowGraphPreflight("Workflow/g", "step-b"); notice != "" || err != nil {
		t.Fatalf("off must skip the check: %q, %v", notice, err)
	}
}
