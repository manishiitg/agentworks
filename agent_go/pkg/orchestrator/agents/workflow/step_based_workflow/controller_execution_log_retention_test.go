package step_based_workflow

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	workspacepkg "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PLAT-176. Execution evidence filenames are built from counters that are local
// to a single dispatch of a step -- `execution-attempt-{retryAttempt}-iteration-
// {loopIteration}`. Both restart at 1 every time the step is dispatched again, so
// a re-dispatched step recomputes the identical path and WriteWorkspaceFile
// silently overwrites the previous dispatch's result, conversation, timing, and
// prompts.
//
// Confirmed live on customer-login (2026-08-22): the browser-capture step was
// dispatched 5 times in one run, and every dispatch clobbered the last. Reading
// the same timing file minutes apart returned two different runs (2675000ms /
// 21 tool calls, then 104248ms / 14 tool calls). The run's own logs could not
// show that the step had run more than once; the history had to be
// reconstructed from server_debug.log instead.
//
// The fix moves the previous dispatch's files into a `superseded/` subfolder
// before writing. Canonical names keep holding the newest dispatch, so every
// existing reader (workflow.go's execution-attempt-* listing, debug_step, the
// archive sweep) is unaffected, and the subfolder keeps prior passes off those
// same top-level globs.

type recordedMove struct{ src, dst string }

type fakeWorkspaceAPI struct {
	mu       sync.Mutex
	existing map[string]bool
	moves    []recordedMove
}

func newFakeWorkspaceAPI(t *testing.T, existing ...string) (*orchestrator.BaseOrchestrator, *fakeWorkspaceAPI) {
	t.Helper()
	fake := &fakeWorkspaceAPI{existing: map[string]bool{}}
	for _, p := range existing {
		fake.existing[p] = true
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/documents/")
		w.Header().Set("Content-Type", "application/json")

		if strings.HasSuffix(path, "/move") && r.Method == http.MethodPost {
			src := strings.TrimSuffix(path, "/move")
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			dst, _ := payload["destination_path"].(string)

			fake.mu.Lock()
			if !fake.existing[src] {
				fake.mu.Unlock()
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"no such file"}`))
				return
			}
			if fake.existing[dst] {
				fake.mu.Unlock()
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"success":false,"error":"destination already exists"}`))
				return
			}
			delete(fake.existing, src)
			fake.existing[dst] = true
			fake.moves = append(fake.moves, recordedMove{src: src, dst: dst})
			fake.mu.Unlock()
			_, _ = w.Write([]byte(`{"success":true}`))
			return
		}

		// Read: used by CheckWorkspaceFileExists.
		fake.mu.Lock()
		ok := fake.existing[path]
		fake.mu.Unlock()
		if !ok {
			_, _ = w.Write([]byte(`{"success":true,"message":"File does not exist","data":{"content":""}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"filepath":"` + path + `","content":"{}"}}`))
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "",
		nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator: %v", err)
	}
	base.WorkspaceClient = workspacepkg.NewClient(server.URL)
	base.SetWorkspacePath("Workflow/test-flow")
	return base, fake
}

func (f *fakeWorkspaceAPI) movedSources() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.moves))
	for _, m := range f.moves {
		out = append(out, m.src)
	}
	return out
}

func (f *fakeWorkspaceAPI) seed(paths ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, path := range paths {
		f.existing[path] = true
	}
}

func TestArchiveSupersededExecutionLogsPreservesAPriorDispatch(t *testing.T) {
	const logDir = "Workflow/test-flow/runs/iteration-0/logs/execute-browser/execution"
	const base = "execution-attempt-1-iteration-1"

	existing := []string{
		logDir + "/" + base + ".json",
		logDir + "/" + base + "-conversation.json",
		logDir + "/" + base + "-timing.json",
		logDir + "/" + base + "-prompts.json",
	}
	bo, fake := newFakeWorkspaceAPI(t, existing...)
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: bo}

	hcpo.archiveSupersededExecutionLogs(context.Background(), logDir, base)

	moved := fake.movedSources()
	if len(moved) != 4 {
		t.Fatalf("expected all 4 evidence files preserved, got %d: %v", len(moved), moved)
	}
	for _, want := range existing {
		found := false
		for _, got := range moved {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("prior dispatch file was left to be overwritten: %s (moved: %v)", want, moved)
		}
	}

	// Every archived file must land under superseded/ so it stays off the
	// top-level `execution-attempt-*` globs readers already use, and must keep
	// a distinguishing stamp so a third dispatch cannot clobber the second.
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, m := range fake.moves {
		if !strings.HasPrefix(m.dst, logDir+"/superseded/") {
			t.Errorf("archived file did not land in superseded/: %s", m.dst)
		}
		if !strings.HasSuffix(m.dst, ".json") {
			t.Errorf("archived file lost its .json extension: %s", m.dst)
		}
		if strings.TrimSuffix(strings.TrimPrefix(m.dst, logDir+"/superseded/"), ".json") == base {
			t.Errorf("archived file kept the canonical name with no stamp, so a later dispatch overwrites it: %s", m.dst)
		}
	}
}

func TestArchiveSupersededExecutionLogsIsANoopOnFirstDispatch(t *testing.T) {
	const logDir = "Workflow/test-flow/runs/iteration-0/logs/execute-browser/execution"
	const base = "execution-attempt-1-iteration-1"

	// Nothing on disk yet: the overwhelmingly common case is a step that runs
	// once. It must not pay a move, and must not log an archive that did not
	// happen.
	bo, fake := newFakeWorkspaceAPI(t)
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: bo}

	hcpo.archiveSupersededExecutionLogs(context.Background(), logDir, base)

	if moved := fake.movedSources(); len(moved) != 0 {
		t.Fatalf("first dispatch must not move anything, got: %v", moved)
	}
}

func TestArchiveSupersededExecutionLogsUsesCollisionProofDestinations(t *testing.T) {
	const logDir = "Workflow/test-flow/runs/iteration-0/logs/execute-browser/execution"
	const base = "execution-attempt-1-iteration-1"
	canonical := []string{
		logDir + "/" + base + ".json",
		logDir + "/" + base + "-conversation.json",
		logDir + "/" + base + "-timing.json",
		logDir + "/" + base + "-prompts.json",
	}
	bo, fake := newFakeWorkspaceAPI(t, canonical...)
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: bo}

	// Archive twice without waiting for the wall-clock second to change. This
	// models two rapid failure/re-dispatch cycles; the real workspace endpoint
	// rejects duplicate destinations, so all eight moves must remain distinct.
	hcpo.archiveSupersededExecutionLogs(context.Background(), logDir, base)
	fake.seed(canonical...)
	hcpo.archiveSupersededExecutionLogs(context.Background(), logDir, base)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.moves) != 8 {
		t.Fatalf("expected both dispatches' four files to be preserved, got %d moves: %v", len(fake.moves), fake.moves)
	}
	destinations := make(map[string]struct{}, len(fake.moves))
	for _, move := range fake.moves {
		if _, duplicate := destinations[move.dst]; duplicate {
			t.Fatalf("archive destination collided across rapid dispatches: %s", move.dst)
		}
		destinations[move.dst] = struct{}{}
	}
}
