package relaypython

import (
	"context"
	"encoding/json"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

// Force process exit while the mailbox read is in flight. The process's
// cancellation must not hide its pending-workflow crash from the supervisor.
type exitingWorkspace struct {
	localWorkspace
	reading chan struct{}
	attempt string
	runPath string
}

func (w *exitingWorkspace) UpdateWorkspaceFile(ctx context.Context, p workspace.UpdateWorkspaceFileParams) (workspace.UpdateFileResult, error) {
	if strings.HasSuffix(p.Filepath, "/runner.py") {
		w.attempt = path.Base(path.Dir(p.Filepath))
	}
	return w.localWorkspace.UpdateWorkspaceFile(ctx, p)
}

func (w *exitingWorkspace) ReadWorkspaceFile(ctx context.Context, p workspace.ReadWorkspaceFileParams) (workspace.ReadFileResult, error) {
	if strings.HasSuffix(p.Filepath, "/call-1.request.json") {
		close(w.reading)
		<-ctx.Done()
		return workspace.ReadFileResult{}, ctx.Err()
	}
	return w.localWorkspace.ReadWorkspaceFile(ctx, p)
}

func (w *exitingWorkspace) ExecuteShellCommand(ctx context.Context, _ workspace.ExecuteShellCommandParams) (workspace.ShellCommandResult, error) {
	select {
	case <-w.reading:
	case <-ctx.Done():
		return workspace.ShellCommandResult{}, ctx.Err()
	}
	raw, _ := json.Marshal(map[string]string{"status": "running", "attempt_id": w.attempt})
	_, err := w.localWorkspace.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: path.Join(w.runPath, "relay_durability.json"), Content: string(raw)})
	if err == nil {
		raw, _ = json.Marshal(map[string]interface{}{"status": "running", "attempt_id": w.attempt, "calls": []interface{}{}})
		_, err = w.localWorkspace.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: path.Join(w.runPath, "relay_trace.json"), Content: string(raw)})
	}
	return workspace.ShellCommandResult{ExitCode: 97}, err
}

func TestDBOSProcessExitDuringMailboxReadPreservesInterruption(t *testing.T) {
	w := &exitingWorkspace{localWorkspace: localWorkspace{t.TempDir()}, reading: make(chan struct{}), runPath: "Workflow/demo/runs/run-1"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := w.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: "Workflow/demo/relay.py", Content: "async def run(INPUT, ctx):\n    return INPUT\n"})
	if err != nil {
		t.Fatal(err)
	}
	err = Run(ctx, Config{Client: w, SourcePath: "Workflow/demo/relay.py", RunPath: w.runPath, Input: map[string]interface{}{}, DBOS: &DBOSConfig{RunID: "run-1", ReleaseHash: "release", Authorize: func(context.Context) error { return nil }}, CallAgent: func(context.Context, Call, ToolCaller) (interface{}, error) {
		t.Error("unexpected agent")
		return nil, nil
	}})
	if !IsInterrupted(err) {
		t.Fatalf("process crash was not recoverable: %v", err)
	}
	var trace map[string]interface{}
	if _, err := read(ctx, w, path.Join(w.runPath, "relay_trace.json"), &trace); err != nil || trace["status"] != "recovering" || trace["error"] != nil {
		t.Fatalf("recoverable exit displayed as a terminal failure: %v %v", trace, err)
	}
}
