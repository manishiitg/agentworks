package step_based_workflow

import (
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGoalReflectionSkillIsNotAttached(t *testing.T) {
	if skills.IsBuiltinSkill("workflow-reflection") {
		t.Fatal("retired reflection procedure is still registered")
	}
	// A saved selection of the removed platform skill must not prevent the
	// remaining skills from loading or resurrect a reflection procedure.
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	loaded := skills.LoadAttachableIn(srv.URL, "Workflow/test", []string{"workflow-reflection", "workflow-database"})
	if len(loaded) != 1 || loaded[0].Name != "workflow-database" {
		t.Fatalf("stale skill selection prevented loading: %+v", loaded)
	}
	c := newAgentClosingTestOrchestrator(t)
	identity, _ := c.assembleStepRuntimeIdentity(&agents.OrchestratorAgentConfig{}, nil, false, []string{"query_workflow_db"}, false, false)
	for _, skill := range identity {
		if skill.Name == "workflow-reflection" {
			t.Fatal("automatic reflection skill attached")
		}
	}

}

func TestLoadWorkflowDBTableNamesExcludesPlatformTables(t *testing.T) {
	// The list exists to show a step where its own observations belong. Naming
	// harness bookkeeping would invite a step to write into Pulse's records.
	for _, name := range []string{"pulse_finding_details", "pulse_review_log", "run_concerns", "report_human_inputs", "eval_results"} {
		if !isPlatformOwnedTable(name) {
			t.Errorf("%q should be filtered out of the step-facing table list", name)
		}
	}
	for _, name := range []string{"replies_posted", "latency_baselines", "cost_daily_metrics", "job_candidates"} {
		if isPlatformOwnedTable(name) {
			t.Errorf("%q is workflow-owned and must stay in the list", name)
		}
	}
}

// An old release can stop after validation, halfway through reflection. Recovery
// must terminate only those retired phases without loading a plan or agent.
func TestGoalReflectionRecoveryEndsPersistedOldPhases(t *testing.T) {
	c := newAgentClosingTestOrchestrator(t)
	raw := `{"workspace_path":"Workflow/test-flow","run_folder":"iteration-3/default","step_id":"research","step_path":"research","agent_session_handle":{"session_id":"original-task"},"phases":{"main_execution":{"status":"completed"},"pre_validation":{"status":"completed"},"direct_learning":{"status":"running"},"kb_review":{"status":"waiting_for_lock"}}}`
	var state WorkflowContinuationState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatal(err)
	}
	// Use the actual constants for historical names, which are the persisted protocol.
	state.Phases[workflowContinuationPhaseMainExecution] = WorkflowContinuationPhaseRecord{Status: workflowContinuationStatusCompleted}
	state.Phases[workflowContinuationPhasePreValidation] = WorkflowContinuationPhaseRecord{Status: workflowContinuationStatusCompleted}
	state.Phases[workflowContinuationPhaseDirectLearning] = WorkflowContinuationPhaseRecord{Status: workflowContinuationStatusRunning}
	state.Phases[workflowContinuationPhaseKBReview] = WorkflowContinuationPhaseRecord{Status: workflowContinuationStatusWaitingForLock}
	bytes, _ := json.Marshal(state)
	raw = string(bytes)
	path := "/api/documents/" + workflowContinuationStatePath(c.GetWorkspacePath(), state.RunFolder, state.StepID, state.StepPath)
	writes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			t.Errorf("recovery tried to load another resource: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPut {
			var body struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			raw = body.Content
			writes++
		} else if r.Method != http.MethodGet {
			t.Errorf("unexpected method %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"content": raw}})
	}))
	defer srv.Close()
	c.WorkspaceClient = workspace.NewClient(srv.URL)
	queued, err := c.queueWorkflowContinuationRecovery(t.Context(), &state)
	if err != nil || queued != 0 || writes != 2 {
		t.Fatalf("queued=%d writes=%d error=%v", queued, writes, err)
	}
	var after WorkflowContinuationState
	if err := json.Unmarshal([]byte(raw), &after); err != nil {
		t.Fatal(err)
	}
	if len(after.PendingRecoveryPhases()) != 0 || workflowContinuationPhaseStatus(&after, workflowContinuationPhaseMainExecution) != workflowContinuationStatusCompleted {
		t.Fatalf("unfinished recovery: %+v", after)
	}
	for _, phase := range []string{workflowContinuationPhaseDirectLearning, workflowContinuationPhaseKBReview} {
		if after.Phases[phase].Status != workflowContinuationStatusSkipped {
			t.Fatalf("retired phase %s still live", phase)
		}
	}
	if after.AgentSessionHandle == nil || after.AgentSessionHandle.SessionID != "original-task" {
		t.Fatal("recovery lost original task handle")
	}
}
