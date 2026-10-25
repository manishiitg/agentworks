package step_based_workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// Writing the changelog file can rewrite workflow.json (a manifest read that migrates it), which logs a change of its
// own through the same writer. It once waited forever on the lock the outer write held.
func TestPlanChangelogWriteInsideAChangelogWriteDoesNotDeadlock(t *testing.T) {
	read := func(context.Context, string) (string, error) { return "", errors.New("not found") }
	var write func(context.Context, string, string) error
	nested := 0
	write = func(ctx context.Context, _, _ string) error {
		nested++
		return writePlanChangelogEntry(ctx, "Workflow/demo", PlanChangelogEntry{Tool: "write_workflow_manifest"}, read, write, loggerv2.NewNoop())
	}
	done := make(chan error, 1)
	go func() {
		done <- writePlanChangelogEntry(context.Background(), "Workflow/demo", PlanChangelogEntry{Tool: "update_step_config"}, read, write, loggerv2.NewNoop())
	}()
	select {
	case err := <-done:
		if err != nil || nested != 1 {
			t.Fatalf("err=%v nested writes=%d", err, nested)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a changelog write made while writing the changelog deadlocked")
	}
}
