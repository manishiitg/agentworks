package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

func TestWritePlanToWorkspaceRejectsInvalidGraphBeforeHTTPWrite(t *testing.T) {
	var requests atomic.Int32
	workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	plan := &todo_creation_human.PlanningResponse{Steps: []todo_creation_human.PlanStepInterface{
		&todo_creation_human.RoutingPlanStep{
			Type: todo_creation_human.StepTypeRouting,
			CommonStepFields: todo_creation_human.CommonStepFields{
				ID:    "router",
				Title: "Router",
			},
			RoutingQuestion: "Where next?",
			Routes: []todo_creation_human.RoutingRoute{
				{RouteID: "a", RouteName: "A", Condition: "a", NextStepID: "deleted-step"},
				{RouteID: "b", RouteName: "B", Condition: "b", NextStepID: "end"},
			},
			DefaultRouteID: "a",
		},
	}}

	err := writePlanToWorkspace(context.Background(), "workflow", plan)
	if err == nil {
		t.Fatal("expected invalid graph to be rejected")
	}
	var validationErr *todo_creation_human.PlanValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want *PlanValidationError", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("workspace API received %d request(s); validation must run before persistence", requests.Load())
	}
}

func TestWritePlanHTTPErrorUsesConflictForRepairableValidation(t *testing.T) {
	recorder := httptest.NewRecorder()
	writePlanHTTPError(recorder, "Step deletion rejected", &todo_creation_human.PlanValidationError{
		Cause: errors.New("PLAN_GRAPH_INVALID: route points to deleted-step"),
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
	if body := recorder.Body.String(); body == "" || !strings.Contains(body, "PLAN_GRAPH_INVALID") {
		t.Fatalf("response body does not expose repairable graph error: %q", body)
	}
}
