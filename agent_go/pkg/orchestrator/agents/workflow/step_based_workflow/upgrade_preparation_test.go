package step_based_workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PLAT-861-310: the native Builder migration must run before 1.0.46's typed
// no-loss checker, while leaving the 1.0.45 manifest and stamp order alone.
func TestUpgradePreparationBeforeTypedChecker(t *testing.T) {
	root := t.TempDir()
	read := func(_ context.Context, p string) (string, error) {
		raw, err := os.ReadFile(filepath.Join(root, strings.TrimPrefix(p, "Workflow/demo/")))
		return string(raw), err
	}
	write := func(_ context.Context, p, content string) error {
		p = filepath.Join(root, strings.TrimPrefix(p, "Workflow/demo/"))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			return err
		}
		return os.WriteFile(p, []byte(content), 0600)
	}
	ctx := context.Background()
	manifest := `{"version":"1.0.45","code_layout_version":1}`
	if err := write(ctx, "Workflow/demo/workflow.json", manifest); err != nil {
		t.Fatal(err)
	}
	if err := write(ctx, "Workflow/demo/planning/plan.json", `{"steps":[{"id":"chat","type":"orchestrator","title":"Greeting","description":"Greet the person once.","items":[]}]}`); err != nil {
		t.Fatal(err)
	}
	check := createCheckPlanNoLossExecutor("Workflow/demo", read)
	if _, err := check(ctx, map[string]interface{}{"step_id": "chat"}); err == nil {
		t.Fatal("fixture did not reproduce the retired-type parse failure")
	}
	prepare := createMigrateAgentStepsExecutor("Workflow/demo", loggerv2.NewNoop(), read, write)
	result, err := prepare(ctx, nil)
	if err != nil || !strings.Contains(result, `"agent_steps":1`) {
		t.Fatalf("prepare: %s %v", result, err)
	}
	if _, err := check(ctx, map[string]interface{}{"step_id": "chat", "proposed_description": "Greet the person once."}); err != nil {
		t.Fatalf("typed checker still cannot read prepared plan: %v", err)
	}
	if result, err := prepare(ctx, nil); err != nil || !strings.Contains(result, "no_op") {
		t.Fatalf("repeated prepare: %s %v", result, err)
	}
	if after, err := read(ctx, "Workflow/demo/workflow.json"); err != nil || after != manifest {
		t.Fatalf("preparation changed the version stamp: %s %v", after, err)
	}
}
