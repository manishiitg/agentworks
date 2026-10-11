package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

func TestHandleGetExecutionLogsReturnsSemanticStepLogs(t *testing.T) {
	const workspacePath = "/workspace/Workflow/test"
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/planning/plan.json": `{
			"steps": [{
				"id": "compile-report",
				"title": "Compile report",
				"context_output": "report.json"
			}]
		}`,
		workspacePath + "/runs/iteration-0/default/logs/compile-report/execution/execution-attempt-1-iteration-0.json": `{"success":true}`,
		workspacePath + "/runs/iteration-0/default/execution/compile-report/report.json":                               `{"rows":3}`,
	}})
	t.Cleanup(workspace.Close)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	request := httptest.NewRequest(
		"GET",
		"/api/workflow/logs?workspace_path="+workspacePath+"&run_folder=iteration-0/default",
		nil,
	)
	response := httptest.NewRecorder()
	(&StreamingAPI{}).handleGetExecutionLogs(response, request)

	if response.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}

	var body struct {
		Success bool `json:"success"`
		Steps   map[string]struct {
			Executions    []map[string]interface{} `json:"executions"`
			OutputContent map[string]interface{}   `json:"output_content"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	step, ok := body.Steps["compile-report"]
	if !ok {
		t.Fatalf("expected semantic step in response, got steps %v", body.Steps)
	}
	if len(step.Executions) != 1 {
		t.Fatalf("expected one execution, got %d", len(step.Executions))
	}
	if step.OutputContent == nil {
		t.Fatal("expected semantic step output content")
	}
}

func TestHandleGetExecutionLogsReturnsAutomaticFinalValidation(t *testing.T) {
	const workspacePath = "/workspace/Workflow/test"
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/planning/plan.json": `{
			"steps": [{"id":"compile-package","title":"Compile package","type":"agent","agent":{"items":[{"id":"execute-and-verify","type":"user_message","message":"Compile and verify the package."}]}}]
		}`,
		workspacePath + "/runs/iteration-0/default/logs/compile-package/execution/execution-attempt-1-iteration-2.json": `{
			"execution_result":"Agent item: __automatic_final_validation__-repair-1 (user_message)"
		}`,
		workspacePath + "/runs/iteration-0/default/logs/compile-package/pre_validation_agent-automatic-final-validation_execution_001_attempt_001.json": `{
			"validation_phase":"agent-automatic-final-validation",
			"execution_attempt":1,
			"validation_attempt":1,
			"overall_pass":false,
			"passed_checks":21,
			"failed_checks":2,
			"errors":[{"Message":"Required field was missing"}]
		}`,
	}})
	t.Cleanup(workspace.Close)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	request := httptest.NewRequest("GET", "/api/workflow/logs?workspace_path="+workspacePath+"&run_folder=iteration-0/default", nil)
	response := httptest.NewRecorder()
	(&StreamingAPI{}).handleGetExecutionLogs(response, request)
	if response.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}

	var body struct {
		Steps map[string]struct {
			PlannedMessages []struct {
				ID      string `json:"id"`
				Message string `json:"message"`
			} `json:"planned_messages"`
			Validations []struct {
				Attempt          int    `json:"attempt"`
				Kind             string `json:"kind"`
				Phase            string `json:"phase"`
				ExecutionAttempt int    `json:"execution_attempt"`
			} `json:"validations"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	validations := body.Steps["compile-package"].Validations
	plannedMessages := body.Steps["compile-package"].PlannedMessages
	if len(plannedMessages) != 1 || plannedMessages[0].ID != "execute-and-verify" || plannedMessages[0].Message != "Compile and verify the package." {
		t.Fatalf("expected planned agent item, got %+v", plannedMessages)
	}
	if len(validations) != 1 {
		t.Fatalf("expected one automatic final validation, got %+v", validations)
	}
	got := validations[0]
	if got.Attempt != 1 || got.Kind != "pre_validation" || got.Phase != "agent-automatic-final-validation" || got.ExecutionAttempt != 1 {
		t.Fatalf("unexpected automatic final validation metadata: %+v", got)
	}
}

func executionLogFolder(path string, children ...virtualtools.WorkspaceFolderItem) virtualtools.WorkspaceFolderItem {
	return virtualtools.WorkspaceFolderItem{
		FilePath: path,
		Type:     "folder",
		Children: children,
	}
}

func TestIsExecutionLogStepFolderSupportsSemanticStepIDs(t *testing.T) {
	metadata := map[string]map[string]string{
		"compile-report": {"title": "Compile report"},
	}

	if !isExecutionLogStepFolder(executionLogFolder("/runs/iteration-0/default/logs/compile-report"), metadata) {
		t.Fatal("expected semantic plan step ID to be recognized as a log folder")
	}
	if isExecutionLogStepFolder(executionLogFolder("/runs/iteration-0/default/logs"), metadata) {
		t.Fatal("did not expect the logs wrapper directory to be treated as a step")
	}
}

func TestIsExecutionLogStepFolderRecognizesHistoricalSemanticLogs(t *testing.T) {
	item := executionLogFolder(
		"/runs/iteration-0/default/logs/old-semantic-step",
		executionLogFolder("/runs/iteration-0/default/logs/old-semantic-step/execution"),
	)

	if !isExecutionLogStepFolder(item, nil) {
		t.Fatal("expected a historical semantic folder with execution logs to be recognized")
	}
}

func TestIsExecutionOutputStepItemSupportsSemanticStepIDs(t *testing.T) {
	metadata := map[string]map[string]string{
		"compile-report": {"title": "Compile report"},
	}
	logs := map[string]map[string]interface{}{
		"historical-step": {},
	}

	for _, path := range []string{
		"/runs/iteration-0/default/execution/compile-report",
		"/runs/iteration-0/default/execution/historical-step",
	} {
		if !isExecutionOutputStepItem(executionLogFolder(path), metadata, logs) {
			t.Fatalf("expected %s to be recognized as a step output", path)
		}
	}

	if isExecutionOutputStepItem(executionLogFolder("/runs/iteration-0/default/execution"), metadata, logs) {
		t.Fatal("did not expect the execution wrapper directory to be treated as a step")
	}
}

func TestPopulateStepMetadataLinksPredefinedRouteToParent(t *testing.T) {
	metadata := make(map[string]map[string]string)
	populateStepMetadata([]map[string]interface{}{{
		"id":    "reel-orchestrator",
		"title": "Master Reel Orchestrator",
		"type":  "todo_task",
		"predefined_routes": []interface{}{map[string]interface{}{
			"route_id": "build-reel",
			"sub_agent_step": map[string]interface{}{
				"id":          "build-reel",
				"title":       "Build Reel",
				"description": "Render the reel.",
			},
		}},
	}}, metadata)

	child := metadata["build-reel"]
	if child["parent_step_id"] != "reel-orchestrator" || child["parent_step_title"] != "Master Reel Orchestrator" || child["route_id"] != "build-reel" {
		t.Fatalf("expected parent and route metadata, got %+v", child)
	}
}

func TestHandleGetExecutionLogsUsesAgentSessionStatus(t *testing.T) {
	const workspacePath = "/workspace/Workflow/test"
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/planning/plan.json": `{
			"steps": [{
				"type": "agent",
				"id": "deliver",
				"title": "Deliver",
				"description": "Deliver the report",
				"messages": [{"id":"send","type":"user_message","message":"send"}]
			}]
		}`,
		workspacePath + "/runs/iteration-0/default/execution/deliver/session.json": `{
  "status":"failed",
  "entries":[{"item_id":"deliver-reflection","status":"failed","summary":"Could not write learning"}]
}`,
	}})
	t.Cleanup(workspace.Close)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	request := httptest.NewRequest("GET", "/api/workflow/logs?workspace_path="+workspacePath+"&run_folder=iteration-0/default", nil)
	response := httptest.NewRecorder()
	(&StreamingAPI{}).handleGetExecutionLogs(response, request)
	if response.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Steps map[string]struct {
			Status  string `json:"agent_status"`
			Session struct {
				Entries []struct {
					ItemID string `json:"item_id"`
					Status string `json:"status"`
				} `json:"entries"`
			} `json:"agent"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := body.Steps["deliver"].Status; got != "failed" {
		t.Fatalf("expected failed agent status, got %q", got)
	}
	if entries := body.Steps["deliver"].Session.Entries; len(entries) != 1 || entries[0].ItemID != "deliver-reflection" || entries[0].Status != "failed" {
		t.Fatalf("expected persisted reflection entry, got %+v", entries)
	}
}

func TestHandleGetExecutionLogsReturnsRoutingEvaluation(t *testing.T) {
	const workspacePath = "/workspace/Workflow/test"
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/planning/plan.json": `{
  "steps": [{"type":"routing","id":"route-job","title":"Route job"}]
}`,
		workspacePath + "/runs/iteration-0/default/logs/route-job/routing-evaluation.json": `{
  "routing_question":"Where should this job go?",
  "selected_route_id":"research",
  "routing_reasoning":"The incoming request requires research.",
  "route_next_steps":{"research":"research-step"},
  "timestamp":"2026-08-25T00:00:00Z"
}`,
	}})
	t.Cleanup(workspace.Close)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	request := httptest.NewRequest("GET", "/api/workflow/logs?workspace_path="+workspacePath+"&run_folder=iteration-0/default", nil)
	response := httptest.NewRecorder()
	(&StreamingAPI{}).handleGetExecutionLogs(response, request)
	if response.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}

	var body struct {
		Steps map[string]struct {
			Orchestration []struct {
				Type              string `json:"type"`
				Source            string `json:"source"`
				RoutingEvaluation struct {
					SelectedRouteID string            `json:"selected_route_id"`
					RouteNextSteps  map[string]string `json:"route_next_steps"`
				} `json:"routing_evaluation"`
			} `json:"orchestration"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	route, ok := body.Steps["route-job"]
	if !ok || len(route.Orchestration) != 1 {
		t.Fatalf("expected routing log in response, got %+v", body.Steps)
	}
	entry := route.Orchestration[0]
	if entry.Type != "routing" || entry.Source != "routing_evaluation" || entry.RoutingEvaluation.SelectedRouteID != "research" || entry.RoutingEvaluation.RouteNextSteps["research"] != "research-step" {
		t.Fatalf("unexpected routing log: %+v", entry)
	}
}

// TestHandleGetExecutionLogsReportsBranchStepTypeNotRouting is the third
// independent PLAT-259 review's finding #2: the shared routing/branch
// executor (executeRoutingStep) writes the exact same routing-evaluation.json
// artifact for either step type, but this handler hardcoded the response
// entry's "type" to "routing" regardless of which one actually produced it --
// so a real branch run rendered as a routing-colored Execution Logs entry
// with "Routing question" even though the owning step's own header said
// Branch. The fix looks up the owning plan step's real type from
// stepMetadata (populated from plan.json) instead of hardcoding.
func TestHandleGetExecutionLogsReportsBranchStepTypeNotRouting(t *testing.T) {
	const workspacePath = "/workspace/Workflow/test"
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/planning/plan.json": `{
  "steps": [{"type":"branch","id":"branch-job","title":"Branch job"}]
}`,
		workspacePath + "/runs/iteration-0/default/logs/branch-job/routing-evaluation.json": `{
  "routing_question":"Which path?",
  "selected_route_id":"research",
  "routing_reasoning":"The incoming request requires research.",
  "route_next_steps":{"research":"research-step"},
  "timestamp":"2026-08-25T00:00:00Z"
}`,
	}})
	t.Cleanup(workspace.Close)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	request := httptest.NewRequest("GET", "/api/workflow/logs?workspace_path="+workspacePath+"&run_folder=iteration-0/default", nil)
	response := httptest.NewRecorder()
	(&StreamingAPI{}).handleGetExecutionLogs(response, request)
	if response.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}

	var body struct {
		Steps map[string]struct {
			Orchestration []struct {
				Type   string `json:"type"`
				Source string `json:"source"`
			} `json:"orchestration"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	route, ok := body.Steps["branch-job"]
	if !ok || len(route.Orchestration) != 1 {
		t.Fatalf("expected branch log in response, got %+v", body.Steps)
	}
	if entry := route.Orchestration[0]; entry.Type != "branch" {
		t.Fatalf("orchestration entry type = %q, want %q (the owning step's real plan.json type)", entry.Type, "branch")
	}
}

// TestHandleGetExecutionLogsPrefersPersistedStepTypeOverCurrentPlan is a
// follow-up finding on the fix above: deriving the entry's type from
// stepMetadata (the CURRENT plan.json) is itself wrong for a HISTORICAL run.
// If a step is later reclassified routing<->branch (e.g. via
// /migrate-routing-to-branch), every one of its past runs would silently
// relabel to the step's new type, even though it actually executed as the
// old one. executeRoutingStep now persists step_type into
// routing-evaluation.json at execution time; this handler must prefer that
// recorded value over the live plan.json lookup.
func TestHandleGetExecutionLogsPrefersPersistedStepTypeOverCurrentPlan(t *testing.T) {
	const workspacePath = "/workspace/Workflow/test"
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		// The step is CURRENTLY typed branch (e.g. migrated after this run).
		workspacePath + "/planning/plan.json": `{
  "steps": [{"type":"branch","id":"migrated-job","title":"Migrated job"}]
}`,
		// But the artifact records what it actually was AT EXECUTION TIME: routing.
		workspacePath + "/runs/iteration-0/default/logs/migrated-job/routing-evaluation.json": `{
  "step_type":"routing",
  "routing_question":"Which path?",
  "selected_route_id":"research",
  "timestamp":"2026-08-25T00:00:00Z"
}`,
	}})
	t.Cleanup(workspace.Close)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	request := httptest.NewRequest("GET", "/api/workflow/logs?workspace_path="+workspacePath+"&run_folder=iteration-0/default", nil)
	response := httptest.NewRecorder()
	(&StreamingAPI{}).handleGetExecutionLogs(response, request)
	if response.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}

	var body struct {
		Steps map[string]struct {
			Orchestration []struct {
				Type string `json:"type"`
			} `json:"orchestration"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	route, ok := body.Steps["migrated-job"]
	if !ok || len(route.Orchestration) != 1 {
		t.Fatalf("expected migrated-job log in response, got %+v", body.Steps)
	}
	if entry := route.Orchestration[0]; entry.Type != "routing" {
		t.Fatalf("orchestration entry type = %q, want %q (the artifact's recorded execution-time type, not the step's current type)", entry.Type, "routing")
	}
}

// TestHandleGetExecutionLogsTagsDownstreamStepsWithSelectedRoute is a
// PLAT-259 follow-up: "route" (routing/branch) was split out as the
// major-sub-workflow-fork concept, but nothing tagged which route a
// downstream step actually belongs to for a given run, so Execution Logs
// couldn't group steps route-wise. Only the step actually reached via this
// run's selected route (per routing-evaluation.json, not merely a route
// the plan declares) should be tagged; the untaken sibling route's step
// must stay untagged.
func TestHandleGetExecutionLogsTagsDownstreamStepsWithSelectedRoute(t *testing.T) {
	const workspacePath = "/workspace/Workflow/test"
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/planning/plan.json": `{
  "steps": [
    {
      "type": "routing",
      "id": "route-job",
      "title": "Route job",
      "routes": [
        {"route_id": "research", "route_name": "Research path", "next_step_id": "research-step"},
        {"route_id": "skip", "route_name": "Skip path", "next_step_id": "skip-step"}
      ]
    },
    {"id": "research-step", "title": "Research step"},
    {"id": "skip-step", "title": "Skip step"}
  ]
}`,
		workspacePath + "/runs/iteration-0/default/logs/route-job/routing-evaluation.json": `{
  "selected_route_id":"research",
  "timestamp":"2026-08-25T00:00:00Z"
}`,
		workspacePath + "/runs/iteration-0/default/logs/research-step/execution/execution-attempt-1-iteration-0.json": `{"success":true}`,
		workspacePath + "/runs/iteration-0/default/logs/skip-step/execution/execution-attempt-1-iteration-0.json":     `{"success":true}`,
	}})
	t.Cleanup(workspace.Close)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	request := httptest.NewRequest("GET", "/api/workflow/logs?workspace_path="+workspacePath+"&run_folder=iteration-0/default", nil)
	response := httptest.NewRecorder()
	(&StreamingAPI{}).handleGetExecutionLogs(response, request)
	if response.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}

	var body struct {
		Steps map[string]struct {
			RouteID        string `json:"route_id"`
			RouteName      string `json:"route_name"`
			RouteKind      string `json:"route_kind"`
			RouteStepID    string `json:"route_step_id"`
			RouteStepTitle string `json:"route_step_title"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	taken, ok := body.Steps["research-step"]
	if !ok {
		t.Fatalf("expected research-step in response, got %+v", body.Steps)
	}
	if taken.RouteID != "research" || taken.RouteName != "Research path" || taken.RouteKind != "routing" ||
		taken.RouteStepID != "route-job" || taken.RouteStepTitle != "Route job" {
		t.Fatalf("research-step not tagged with the selected route: %+v", taken)
	}

	untaken, ok := body.Steps["skip-step"]
	if !ok {
		t.Fatalf("expected skip-step in response, got %+v", body.Steps)
	}
	if untaken.RouteID != "" || untaken.RouteKind != "" {
		t.Fatalf("skip-step (the untaken sibling route) must not be tagged: %+v", untaken)
	}
}
