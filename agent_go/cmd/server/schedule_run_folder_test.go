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
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

func TestAllocateScheduledRunFolderIsPermanentAndIndependentOfOtherRuns(t *testing.T) {
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
	if !permanentRunFolderPattern.MatchString(first) {
		t.Fatalf("first folder = %q, want permanent run identity", first)
	}
	again, err := allocateScheduledRunFolder("Workflow/demo", "run-one")
	if err != nil || again != first {
		t.Fatalf("idempotent allocation = (%q, %v), want (%q, nil)", again, err, first)
	}
	second, err := allocateScheduledRunFolder("Workflow/demo", "run-two")
	if err != nil {
		t.Fatal(err)
	}
	if second == first || !permanentRunFolderPattern.MatchString(second) {
		t.Fatalf("second folder = %q, want another permanent run", second)
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
		"version": 3, "runs": folders,
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

// PLAT-746-384: converted workflows have no physical Workflow/<name> root.
// Timer, webhook/function and Slack starts must allocate in Crew.Private.
func TestBoundWorkflowNonmanualRunFoldersUsePrivateRuntime(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	binding := crewExecutableBinding{ID: "task", CrewID: "crew-lab", CrewRoot: "Crew/lab", Root: "Workflow/task", Kind: "workflow"}
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "lab", ProjectID: binding.CrewID, OwnerID: "owner"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(docs, binding.CrewRoot), 0700); err != nil {
		t.Fatal(err)
	}
	if err := registerCrewExecutable(binding); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(docs, crewroot.PrivateDirectory, binding.CrewID), 0700); err != nil {
		t.Fatal(err)
	}
	layout, found, err := crewroot.LookupExecutable(docs, binding.Root)
	if err != nil || !found {
		t.Fatalf("resolve binding: %v", err)
	}
	if err := os.MkdirAll(layout.Runtime, 0700); err != nil {
		t.Fatal(err)
	}
	for _, producer := range []struct {
		name     string
		allocate func(string, string) (string, error)
		marker   string
	}{
		{"schedule", allocateScheduledRunFolder, ".schedule-run-id"},
		{"webhook", allocateWebhookRunFolder, ".webhook-run-id"},
		{"slack", allocateSlackRunFolder, ".slack-run-id"},
	} {
		t.Run(producer.name, func(t *testing.T) {
			id := "bound-" + producer.name
			folder, err := producer.allocate(binding.Root, id)
			if err != nil {
				t.Fatal(err)
			}
			marker, err := os.ReadFile(filepath.Join(layout.Runtime, "runs", folder, producer.marker))
			if err != nil || string(marker) != id {
				t.Fatalf("private invocation marker: %q %v", marker, err)
			}
			again, err := producer.allocate(binding.Root, id)
			if err != nil || again != folder {
				t.Fatalf("retry changed run folder: %q %v", again, err)
			}
			if producer.name == "webhook" {
				output := "execution/task/output/result.txt"
				file := filepath.Join(layout.Runtime, "runs", folder, filepath.FromSlash(output))
				if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte("completed"), 0600); err != nil {
					t.Fatal(err)
				}
				call := &crewFunctionCall{TargetKind: triggerCallerWorkflow, TargetPath: binding.Root, WorkflowRunFolder: folder, RunID: id, Files: []structuredFunctionFile{{Name: output}}}
				root, err := openStructuredFunctionOutputRoot(call, output)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := root.ReadFile(output)
				root.Close()
				if err != nil || string(raw) != "completed" {
					t.Fatalf("function output: %q %v", raw, err)
				}
				call.RunID = "another-run"
				if root, err := openStructuredFunctionOutputRoot(call, output); err == nil {
					root.Close()
					t.Fatal("accepted another invocation's output")
				}
			}
		})
	}
	if _, err := os.Stat(filepath.Join(docs, binding.Root)); !os.IsNotExist(err) {
		t.Fatalf("recreated standalone workflow: %v", err)
	}
	// Runtime storage itself must not be replaced by a symlink to another root.
	if err := os.Rename(layout.Runtime, layout.Runtime+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), layout.Runtime); err != nil {
		t.Fatal(err)
	}
	if root, err := webhookWorkspaceRoot(binding.Root); err == nil {
		root.Close()
		t.Fatal("followed a planted runtime symlink")
	}
}
