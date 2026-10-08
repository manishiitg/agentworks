package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPulseFixRunWorklistMakesTechnicalDue(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	ctx := context.Background()
	ws := "Workflow/fix-worklist"
	planningDir := filepath.Join(root, ws, "planning")
	if err := os.MkdirAll(planningDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planningDir, "plan.json"), []byte(`{"steps":[{"id":"step-a","type":"regular"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planningDir, "step_config.json"), []byte(`{"steps":[{"id":"step-a"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := recordPulseFixRunWorklist(ctx, ws, "fix-1", "1 open issue(s)"); err != nil {
		t.Fatalf("record fix-run worklist: %v", err)
	}
	for module, want := range map[string]bool{
		pulseModuleTechnicalReview:    true,
		pulseModuleStrategicReview:    false,
		pulseModuleArchitectureReview: false,
	} {
		if due, err := pulseWorklistModulesDue(ctx, ws, "fix-1", module); err != nil || due != want {
			t.Fatalf("%s due = %v (%v), want %v", module, due, err, want)
		}
	}
}
