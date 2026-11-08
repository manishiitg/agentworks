package server

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWebhookRunResultDiscardsSnapshotFromBeforeRecovery(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	const workspace = "Workflow/recovered-workflow"
	const folder = "iteration-1-hook"
	root := filepath.Join(docs, workspace, "runs", folder)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".webhook-run-id"), []byte("recovered-run"), 0600); err != nil {
		t.Fatal(err)
	}
	// PLAT-746-474: a slot could not read the 0600 result file. Keep this
	// service record group-readable without changing the writable run root.
	if err := os.Chmod(root, os.ModeSetgid|0770); err != nil {
		t.Fatal(err)
	}
	interruptedAt := time.Now().Add(-time.Minute)
	run := schedulerstate.Run{RunID: "recovered-run", RunFolder: folder, State: schedulerstate.StateInterrupted, CompletedAt: &interruptedAt, ErrorMessage: "interrupted: server restarted"}
	cached, err := readWebhookRunResult(workspace, run)
	if err != nil || cached.Status != "interrupted" {
		t.Fatalf("initial snapshot: %+v %v", cached, err)
	}
	if info, err := os.Stat(filepath.Join(root, ".webhook-result.json")); err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("result is not slot-readable: %v %v", info, err)
	}
	if info, err := os.Stat(root); err != nil || info.Mode().Perm() != 0770 {
		t.Fatalf("result writer changed run permissions: %v %v", info, err)
	}
	completedAt := time.Now()
	run.State, run.CompletedAt, run.ErrorMessage = schedulerstate.StateCompleted, &completedAt, ""
	result, err := readWebhookRunResult(workspace, run)
	if err != nil || result.Status != "completed" || result.Error != "" || !result.FinishedAt.Equal(completedAt) {
		t.Fatalf("recovered ledger hidden by stale snapshot: %+v %v", result, err)
	}
	again, err := readWebhookRunResult(workspace, run)
	if err != nil || again.Status != "completed" {
		t.Fatalf("replacement snapshot: %+v %v", again, err)
	}
}
