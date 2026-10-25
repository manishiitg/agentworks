package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

func TestAllocateScheduledRunFolderIsImmutableAndSharesNumericNamespace(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	runs := filepath.Join(docs, "Workflow", "demo", "runs")
	for _, name := range []string{"iteration-4", "iteration-8-hook", "iteration-6-sched"} {
		if err := os.MkdirAll(filepath.Join(runs, name), 0700); err != nil {
			t.Fatal(err)
		}
	}

	first, err := allocateScheduledRunFolder("Workflow/demo", "run-one")
	if err != nil {
		t.Fatal(err)
	}
	if first != "iteration-9-sched" {
		t.Fatalf("first folder = %q, want iteration-9-sched", first)
	}
	again, err := allocateScheduledRunFolder("Workflow/demo", "run-one")
	if err != nil || again != first {
		t.Fatalf("idempotent allocation = (%q, %v), want (%q, nil)", again, err, first)
	}
	second, err := allocateScheduledRunFolder("Workflow/demo", "run-two")
	if err != nil {
		t.Fatal(err)
	}
	if second != "iteration-10-sched" {
		t.Fatalf("second folder = %q, want iteration-10-sched", second)
	}
}

func TestScheduledRetentionKeepsTenCompletedRunsAndHistory(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	workspace := "Workflow/demo"
	root := filepath.Join(docs, workspace)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflow.json"), []byte(`{"schema_version":1,"id":"demo","label":"Demo"}`), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := schedulerstate.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := &SchedulerService{stateStore: store}
	ctx := context.Background()
	var folders []string
	for i := 0; i < 12; i++ {
		runID := fmt.Sprintf("schedule-run-%d", i)
		folder, allocationErr := allocateScheduledRunFolder(workspace, runID)
		if allocationErr != nil {
			t.Fatal(allocationErr)
		}
		folders = append(folders, folder)
		if err := store.BeginRun(ctx, schedulerstate.Run{RunID: runID, ScopeType: "workflow", ScopeID: workspace, LockKey: runID, ScheduleID: "daily", TriggerSource: "cron"}); err != nil {
			t.Fatal(err)
		}
		if err := store.AssignRunFolder(ctx, runID, folder); err != nil {
			t.Fatal(err)
		}
		if err := store.Transition(ctx, schedulerstate.Transition{RunID: runID, To: schedulerstate.StateFailed, At: time.Now().Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	index := map[string]interface{}{
		"version": 2, "active_iteration": "iteration-0", "retained_iterations": folders,
		"scheduled_iterations": folders, "webhook_iterations": []string{},
	}
	encoded, _ := json.Marshal(index)
	if err := os.WriteFile(filepath.Join(root, "runs", "run_index.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if err := svc.pruneScheduledRunsChecked(workspace); err != nil {
		t.Fatal(err)
	}
	for i, folder := range folders {
		_, runErr := os.Stat(filepath.Join(root, "runs", folder))
		if i < 2 {
			if !os.IsNotExist(runErr) || !scheduleArtifactsExpired(workspace, fmt.Sprintf("schedule-run-%d", i)) {
				t.Fatalf("expired scheduled evidence was retained: %s run=%v", folder, runErr)
			}
		} else if runErr != nil {
			t.Fatalf("new scheduled evidence was removed: %s run=%v", folder, runErr)
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, "runs", "run_index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), folders[0]) || strings.Contains(string(raw), folders[1]) {
		t.Fatalf("expired folders remain in run index: %s", raw)
	}
	if _, err := store.GetRun(ctx, "schedule-run-0"); err != nil {
		t.Fatal("durable schedule history was removed")
	}
}

func TestAllocateScheduledRunFolderConcurrentOccurrencesDoNotCollide(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	if err := os.MkdirAll(filepath.Join(docs, "Workflow", "demo"), 0700); err != nil {
		t.Fatal(err)
	}

	const count = 12
	results := make(chan string, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			folder, err := allocateScheduledRunFolder("Workflow/demo", fmt.Sprintf("run-%d", index))
			if err != nil {
				errs <- err
				return
			}
			results <- folder
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for folder := range results {
		if seen[folder] {
			t.Fatalf("duplicate folder allocation: %s", folder)
		}
		seen[folder] = true
	}
	if len(seen) != count {
		t.Fatalf("allocated %d folders, want %d", len(seen), count)
	}
}
