package step_based_workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	workspacepkg "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// This is the fail-before/pass-after regression test for the notification seen
// live on the "instagram" workflow (2026-08-23): step-create-reel's own log
// folder held execution-attempt-*.json files from three unrelated dispatches
// (2026-06-30, 2026-07-17, and today's real run), never cleaned up. attempt/
// iteration counters are local to one dispatch (same shape as PLAT-176) --
// across dispatches they are not a time-ordered sequence. The old selection
// logic picked whichever file had the numerically highest (attempt,
// iteration), so the 2026-07-17 dispatch (attempt 3, which genuinely failed
// pre-validation three times that day) silently outranked and replaced
// today's attempt-1 dispatch, which actually succeeded -- resurrecting a
// month-old failure as if it were the current result.
func newFakeWorkspaceAPIWithContent(t *testing.T, content map[string]string) *orchestrator.BaseOrchestrator {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/documents/")
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPut {
			var body struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			content[path] = body.Content
			_, _ = w.Write([]byte(`{"success":true}`))
			return
		}

		body, ok := content[path]
		if !ok {
			_, _ = w.Write([]byte(`{"success":true,"message":"File does not exist","data":{"content":""}}`))
			return
		}
		envelope := map[string]interface{}{
			"success": true,
			"data":    map[string]interface{}{"filepath": path, "content": body},
		}
		encoded, err := json.Marshal(envelope)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(encoded)
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
	base.SetWorkspacePath("Workflow/instagram")
	return base
}

func TestLoadSingleStepResultFromLogsPrefersRecentCompletionOverHigherAttemptNumber(t *testing.T) {
	const logDir = "Workflow/instagram/runs/test-run/logs/step-create-reel/execution"

	content := map[string]string{
		logDir + "/execution-attempt-1-iteration-0.json": `{
			"attempt": 1, "iteration": 0,
			"completed_at": "2026-08-23T00:52:04Z",
			"execution_result": "STATUS: COMPLETED"
		}`,
		logDir + "/execution-attempt-2-iteration-0.json": `{
			"attempt": 2, "iteration": 0,
			"completed_at": "2026-07-17T09:57:51Z",
			"execution_result": "CONCERNS: Validation history: attempt 1 failed - stale July result"
		}`,
		logDir + "/execution-attempt-3-iteration-0.json": `{
			"attempt": 3, "iteration": 0,
			"completed_at": "2026-07-17T09:59:27Z",
			"execution_result": "CONCERNS: Validation history: attempt 1 failed - ...; attempt 2 failed - ...; unresolved after 3 attempt(s)."
		}`,
	}
	bo := newFakeWorkspaceAPIWithContent(t, content)
	bo.SetWorkspacePath("Workflow/instagram")
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: bo, selectedRunFolder: "test-run"}
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&ScriptedPlanStep{
			Type:             StepTypeScripted,
			CommonStepFields: CommonStepFields{ID: "step-create-reel", Title: "Master Reel Orchestrator"},
		},
	}}

	result, found := hcpo.loadSingleStepResultFromLogs(withExecutionPlan(t.Context(), plan), 1)
	if !found {
		t.Fatal("expected a result to be found")
	}
	if result != "STATUS: COMPLETED" {
		t.Fatalf("loadSingleStepResultFromLogs returned a stale, superseded result instead of today's real one:\n%s", result)
	}
}

func TestLoadSingleStepResultFromLogsFallsBackToAttemptOrderingWithoutTimestamps(t *testing.T) {
	const logDir = "Workflow/instagram/runs/test-run/logs/step-create-reel/execution"

	// Legacy files predating completed_at being recorded: the old ordering
	// (highest attempt/iteration wins) is the only signal available and must
	// still work, so this is not a regression for pre-existing evidence.
	content := map[string]string{
		logDir + "/execution-attempt-1-iteration-0.json": `{"attempt": 1, "iteration": 0, "execution_result": "first"}`,
		logDir + "/execution-attempt-2-iteration-0.json": `{"attempt": 2, "iteration": 0, "execution_result": "second"}`,
	}
	bo := newFakeWorkspaceAPIWithContent(t, content)
	bo.SetWorkspacePath("Workflow/instagram")
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: bo, selectedRunFolder: "test-run"}
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&ScriptedPlanStep{
			Type:             StepTypeScripted,
			CommonStepFields: CommonStepFields{ID: "step-create-reel", Title: "Master Reel Orchestrator"},
		},
	}}

	result, found := hcpo.loadSingleStepResultFromLogs(withExecutionPlan(t.Context(), plan), 1)
	if !found {
		t.Fatal("expected a result to be found")
	}
	if result != "second" {
		t.Fatalf("expected the fallback ordering to still prefer the higher attempt number, got %q", result)
	}
}
