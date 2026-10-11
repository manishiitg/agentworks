package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the offline command's real filesystem path: preflight must finish
// before any write, and a checkpoint must never hide an unmigrated plan.
func TestWorkflowAgentDeploymentBatchPreflightsAndStampsLast(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan := `{"steps":[{"id":"review","type":"orchestrator","title":"Review","description":"Review","next_step_id":"end"}],"custom":{"keep":true}}`
	manifest := `{"id":"example","version":"1.0.46","code_layout_version":1,"custom":9007199254740993}`
	save := func(folder, body, config string) string {
		t.Helper()
		dir := filepath.Join(root, folder)
		if err := os.MkdirAll(filepath.Join(dir, "planning"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "planning/plan.json"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "workflow.json"), []byte(config), 0600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	a := save("Workflow/a", plan, manifest)
	b := save("Workflow/b", plan, `{"version":"1.0.45","code_layout_version":1}`)
	historical := save("Workflow/a/runs/iteration-1", plan, manifest)
	pinned := save("Workflow/.relay_releases/example/v1", plan, manifest)
	relay := save("Workflow/relay", plan, `{"kind":"relay","version":"1.0.1"}`)
	var out bytes.Buffer
	if _, err := migrateWorkflowAgentStepsFromDisk([]string{root}, true, false, &out); err == nil {
		t.Fatal("apply without a drain admitted")
	}
	if _, err := migrateWorkflowAgentStepsFromDisk([]string{root}, true, true, &out); err == nil || !strings.Contains(err.Error(), "1.0.46") {
		t.Fatalf("earlier checkpoint admitted: %v", err)
	}
	for _, name := range []string{a, b} {
		raw, _ := os.ReadFile(filepath.Join(name, "planning/plan.json"))
		if string(raw) != plan {
			t.Fatal("failed batch changed a plan")
		}
	}
	if err := os.WriteFile(filepath.Join(b, "workflow.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if n, err := migrateWorkflowAgentStepsFromDisk([]string{root}, false, false, &out); err != nil || n != 2 {
		t.Fatalf("dry-run %d %v", n, err)
	}
	original, _ := os.ReadFile(filepath.Join(a, "planning/plan.json"))
	if string(original) != plan {
		t.Fatal("dry-run wrote")
	}
	if n, err := migrateWorkflowAgentStepsFromDisk([]string{root}, true, true, &out); err != nil || n != 2 {
		t.Fatalf("apply %d %v", n, err)
	}
	for _, dir := range []string{a, b} {
		raw, _ := os.ReadFile(filepath.Join(dir, "planning/plan.json"))
		if !strings.Contains(string(raw), `"type": "agent"`) || strings.Contains(string(raw), `"orchestrator"`) {
			t.Fatalf("legacy plan: %s", raw)
		}
		cfg, _ := os.ReadFile(filepath.Join(dir, "workflow.json"))
		if !strings.Contains(string(cfg), "1.0.47") || !strings.Contains(string(cfg), "9007199254740993") || !strings.Contains(string(cfg), "contract_upgrade_history") {
			t.Fatalf("stamp lost metadata: %s", cfg)
		}
		backup, _ := os.ReadFile(filepath.Join(dir, "planning/plan.json.pre-agent-v1.0.47.bak"))
		if string(backup) != plan {
			t.Fatal("original backup missing")
		}
		receipts, _ := filepath.Glob(filepath.Join(dir, "planning/changelog/*.json"))
		if len(receipts) != 1 {
			t.Fatal("audit receipt missing")
		}
		var receipt map[string]any
		data, _ := os.ReadFile(receipts[0])
		if json.Unmarshal(data, &receipt) != nil {
			t.Fatal("invalid audit JSON")
		}
	}
	for _, dir := range []string{historical, pinned, relay} {
		raw, _ := os.ReadFile(filepath.Join(dir, "planning/plan.json"))
		if string(raw) != plan {
			t.Fatal("historical or Relay plan changed")
		}
	}
	if n, err := migrateWorkflowAgentStepsFromDisk([]string{root}, true, true, &out); err != nil || n != 0 {
		t.Fatalf("retry %d %v", n, err)
	}
	// An already-stamped legacy plan is a broken checkpoint, not a valid no-op.
	if err := os.WriteFile(filepath.Join(a, "planning/plan.json"), []byte(plan), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := migrateWorkflowAgentStepsFromDisk([]string{root}, false, false, &out); err == nil || !strings.Contains(err.Error(), "inconsistent checkpoint") {
		t.Fatalf("bad stamp admitted: %v", err)
	}
}
