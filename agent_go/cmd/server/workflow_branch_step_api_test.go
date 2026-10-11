package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// TestHandleAddStepAcceptsBranchStep is the third independent PLAT-259
// review's finding #3: the frontend's PlanStep union and
// usePlanData.addStep/updateStep APIs accept a branch step, but this
// platform-generic plan-mutation HTTP endpoint (distinct from the
// Builder-native add_branch_step/update_branch_step tools) only unmarshaled
// "scripted" and "todo_task", returning "Unknown step type" for a branch
// payload -- an internally inconsistent contract between what the frontend
// offers and what this API path actually accepts.
func TestHandleAddStepAcceptsBranchStep(t *testing.T) {
	const workspacePath = "/workspace/Workflow/test"
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/planning/plan.json": `{"steps":[]}`,
	}})
	t.Cleanup(workspace.Close)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	reqBody, err := json.Marshal(AddStepRequest{
		WorkspacePath: workspacePath,
		Step: map[string]interface{}{
			"type":            "branch",
			"id":              "branch-step",
			"title":           "Branch Step",
			"branch_question": "Which path?",
			"routes": []map[string]interface{}{
				{"route_id": "route-a", "route_name": "A", "condition": "c", "next_step_id": "end"},
				{"route_id": "route-b", "route_name": "B", "condition": "c", "next_step_id": "end"},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	request := httptest.NewRequest("POST", "/api/workflow/plan/add-step", bytes.NewReader(reqBody))
	response := httptest.NewRecorder()
	(&StreamingAPI{}).handleAddStep(response, request)

	if response.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}

	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Step map[string]interface{} `json:"step"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Success || body.Data.Step["type"] != "branch" {
		t.Fatalf("expected a successfully added branch step, got %+v", body)
	}

	// Confirm it actually persisted to plan.json, not just echoed back.
	plan, err := readPlanFromWorkspace(request.Context(), workspacePath)
	if err != nil {
		t.Fatalf("read plan back: %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("expected 1 persisted step, got %d", len(plan.Steps))
	}
	if _, ok := plan.Steps[0].(*todo_creation_human.BranchPlanStep); !ok {
		t.Fatalf("persisted step = %T, want *BranchPlanStep", plan.Steps[0])
	}
}

// TestUpdateStepInPlanAcceptsBranchStep covers the update side of the same
// finding: updateStepInPlan's type switch had no *BranchPlanStep case and
// hit the "unknown step type" default, even for a common-field update
// (title/description) that every other step type already supports through
// this path.
func TestUpdateStepInPlanAcceptsBranchStep(t *testing.T) {
	plan := &todo_creation_human.PlanningResponse{
		Steps: []todo_creation_human.PlanStepInterface{
			&todo_creation_human.BranchPlanStep{
				CommonStepFields: todo_creation_human.CommonStepFields{ID: "branch-step", Title: "Old Title"},
				BranchQuestion:   "Which path?",
			},
		},
	}
	newTitle := "New Title"
	err := updateStepInPlan(plan, "branch-step", &PlanStepUpdate{Title: &newTitle})
	if err != nil {
		t.Fatalf("updateStepInPlan rejected a branch step: %v", err)
	}
	updated, ok := plan.Steps[0].(*todo_creation_human.BranchPlanStep)
	if !ok {
		t.Fatalf("updated step = %T, want *BranchPlanStep", plan.Steps[0])
	}
	if updated.Title != newTitle {
		t.Fatalf("updated.Title = %q, want %q", updated.Title, newTitle)
	}
}
