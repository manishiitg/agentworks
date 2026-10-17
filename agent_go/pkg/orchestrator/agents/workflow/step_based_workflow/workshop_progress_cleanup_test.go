package step_based_workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	orchestrator_events "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	baseevents "github.com/manishiitg/mcpagent/events"
)

type canceledRetryNotifier struct{ finished chan struct{} }

func (n *canceledRetryNotifier) OnExecutionStart(start WorkshopExecutionStart) {
	if start.Cancel != nil {
		// Admit a fresh execution through the real tool, then stop it before
		// any model or external action. The bug was refusal before admission.
		start.Cancel()
	}
}
func (n *canceledRetryNotifier) OnExecutionComplete(id, _, _ string, _ map[string]string, _ error) {
	if strings.HasPrefix(id, "exec-") {
		close(n.finished)
	}
}
func (*canceledRetryNotifier) OnExecutionTerminated(_, _ string) {}

// A quota-failed full workflow left a progress mirror running after its real
// worker stopped. execute_step refused retry and stop_step had no worker to
// cancel. A cleanup timeout must still protect any worker that has NOT stopped.
func TestFinishedWorkflowProgressReleasesRetryOnlyAfterOwnedChildrenStop(t *testing.T) {
	controller := newAgentClosingTestOrchestrator(t)
	files := map[string]string{
		"workflow.json":             `{"code_layout_version":1}`,
		"variables/variables.json":  `{"variables":[],"groups":[]}`,
		"planning/plan.json":        `{"steps":[{"id":"research","title":"Research","type":"agent","description":"Research.","items":[{"id":"work","type":"user_message","message":"Research."}]}]}`,
		"planning/step_config.json": `{"steps":[]}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, exists := files[strings.TrimPrefix(r.URL.Path, "/api/documents/Workflow/test-flow/")]
		if !exists || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{"content": body}})
	}))
	defer server.Close()
	controller.WorkspaceClient = workspace.NewClient(server.URL)
	controller.variableManager = NewVariableManager(controller.BaseOrchestrator)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	registry := NewWorkshopStepRegistry()
	notifier := &canceledRetryNotifier{finished: make(chan struct{})}
	sessionCtx, shutdown := context.WithCancel(context.Background())
	defer shutdown()
	session := &WorkshopChatSession{controller: controller, StepRegistry: registry, sessionCtx: sessionCtx, executionNotifier: notifier, config: &WorkshopConfig{WorkspacePath: controller.GetWorkspacePath()}}
	cleanup := &workshopSubAgentCleanup{}
	bridge := &workflowProgressBridge{session: session, parentID: "workflow-full-failed", ownedSubAgents: cleanup}
	if err := bridge.HandleEvent(t.Context(), &baseevents.AgentEvent{
		Type: orchestrator_events.OrchestratorAgentStart,
		Data: &orchestrator_events.OrchestratorAgentStartEvent{AgentType: "generic_execution", AgentName: "research", StepIndex: 0},
	}); err != nil {
		t.Fatal(err)
	}
	stale, found, _ := registry.LatestSnapshotForStep("research")
	if !found {
		t.Fatal("progress mirror was not registered")
	}
	registry.Register(&WorkshopStepExecution{ID: "other-parent-step", StepID: "other", Status: WorkshopStepRunning})
	registry.Register(&WorkshopStepExecution{ID: "finished", StepID: "finished", Status: WorkshopStepDone, Result: "real outcome"})
	bridge.progressID["finished"] = "finished"
	execCtx := &SubAgentExecutionContext{ParentContext: context.Background()}
	cleanup.track(execCtx)
	childCtx, call := execCtx.registerAsyncCall(t.Context(), "owned-child", "research", "", "generic")
	defer call.cancel()
	(&workshopSubAgentNotifier{registry: registry}).OnSubAgentStart(WorkshopExecutionStart{ID: call.ExecutionID, Name: "Research child", Cancel: call.cancel})
	execCtx.runAsyncCall(call, func() (string, error) {
		<-childCtx.Done()
		panic("worker failed while stopping")
	})
	draft := newWorkshopDefinitionDraft()
	RegisterWorkshopChatTools(draft, session, workshopToolTestLogger{})
	retry := draft.tools["execute_step"].Execute
	args := map[string]interface{}{"step_id": "research", "group_name": "default"}
	assertBlocked := func() {
		t.Helper()
		result, err := retry(t.Context(), args)
		if err != nil || !strings.Contains(result, "ALREADY RUNNING") || !strings.Contains(result, stale.ID) {
			t.Fatalf("retry must retain the live owner's guard: %q, %v", result, err)
		}
	}
	assertBlocked()
	settled := bridge.finishProgressAfterChildrenStop()
	assertBlocked() // Parent exit/cleanup timeout alone is insufficient.
	stop := draft.tools["stop_step"].Execute
	queryResult, queryErr := draft.tools["query_step"].Execute(t.Context(), map[string]interface{}{"execution_id": call.ExecutionID})
	if queryErr != nil || !strings.Contains(strings.ToLower(queryResult), "running") {
		t.Fatalf("owned child must be queryable as running before stop: %q, %v", queryResult, queryErr)
	}
	stopResult, stopErr := stop(t.Context(), map[string]interface{}{"execution_id": call.ExecutionID})
	if stopErr != nil || !strings.Contains(stopResult, "has been canceled") {
		t.Fatalf("actual retained child must remain individually stoppable: %q, %v", stopResult, stopErr)
	}
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
		t.Fatal("stopped worker did not release its orphan progress mirror")
	}
	if snap, _ := registry.GetSnapshot(stale.ID); snap.Status != WorkshopStepFailed || snap.Err == nil || !strings.Contains(snap.Err.Error(), "without an end event") {
		t.Fatalf("missing end must be recorded honestly as failure: %+v", snap)
	}
	if snap, _ := registry.GetSnapshot("other-parent-step"); snap.Status != WorkshopStepRunning {
		t.Fatal("another parent's progress was changed")
	}
	if snap, _ := registry.GetSnapshot("finished"); snap.Status != WorkshopStepDone || snap.Result != "real outcome" {
		t.Fatal("a real terminal outcome was overwritten")
	}
	result, err := retry(t.Context(), args)
	if err != nil || !strings.Contains(result, "started in background") || strings.Contains(result, "ALREADY RUNNING") {
		t.Fatalf("fresh retry was not admitted after worker exit: %q, %v", result, err)
	}
	select {
	case <-notifier.finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the admitted, immediately canceled retry did not finish")
	}
	// A worker that never exits must not leave a cleanup waiter writing into
	// the registry after this session closes during server shutdown.
	otherCtx := &SubAgentExecutionContext{ParentContext: context.Background()}
	_, otherCall := otherCtx.registerAsyncCall(t.Context(), "unresponsive-child", "other", "", "generic")
	defer otherCall.cancel()
	otherCleanup := &workshopSubAgentCleanup{}
	otherCleanup.track(otherCtx)
	other := &workflowProgressBridge{session: session, parentID: "other-parent", ownedSubAgents: otherCleanup, progressID: map[string]string{"other": "other-parent-step"}}
	aborted := other.finishProgressAfterChildrenStop()
	shutdown()
	select {
	case <-aborted:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup waiter survived session shutdown")
	}
	otherCtx.completeAsyncCall(otherCall, "", context.Canceled)
	if snap, _ := registry.GetSnapshot("other-parent-step"); snap.Status != WorkshopStepRunning {
		t.Fatal("cleanup wrote a terminal projection after shutdown")
	}
}
