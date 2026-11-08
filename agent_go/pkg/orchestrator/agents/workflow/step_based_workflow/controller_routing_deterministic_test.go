package step_based_workflow

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestParseRouteSelectionPayloadAcceptsCanonicalAndLegacyFields(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "canonical select_route",
			content: `{"select_route":"search"}`,
			want:    "search",
		},
		{
			name:    "legacy route_id",
			content: `{"route_id":"route-search"}`,
			want:    "route-search",
		},
		{
			name:    "selected_route_id compatibility",
			content: `{"selected_route_id":"route-save"}`,
			want:    "route-save",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRouteSelectionPayload(tt.content)
			if err != nil {
				t.Fatalf("parseRouteSelectionPayload returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseRouteSelectionPayload = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseRouteSelectionPayloadRejectsMissingSelection(t *testing.T) {
	_, err := parseRouteSelectionPayload(`{"mode":"search"}`)
	if err == nil {
		t.Fatal("expected error for payload without route selection field")
	}
	if !strings.Contains(err.Error(), "select_route") {
		t.Fatalf("expected error to mention accepted fields, got %v", err)
	}
}

func TestResolveRouteSelectionValueMatchesRouteID(t *testing.T) {
	routes := deterministicRoutingTestRoutes()

	got, err := resolveRouteSelectionValue(routes, "route-search")
	if err != nil {
		t.Fatalf("resolveRouteSelectionValue returned error: %v", err)
	}
	if got != "route-search" {
		t.Fatalf("resolveRouteSelectionValue = %q, want route-search", got)
	}
}

func TestResolveRouteSelectionValueMatchesUniqueNextStepID(t *testing.T) {
	routes := deterministicRoutingTestRoutes()

	got, err := resolveRouteSelectionValue(routes, "step-save")
	if err != nil {
		t.Fatalf("resolveRouteSelectionValue returned error: %v", err)
	}
	if got != "route-save" {
		t.Fatalf("resolveRouteSelectionValue = %q, want route-save", got)
	}
}

func TestResolveRouteSelectionValueRejectsAmbiguousNextStepID(t *testing.T) {
	routes := []RoutingRoute{
		{RouteID: "route-a", NextStepID: "step-shared"},
		{RouteID: "route-b", NextStepID: "step-shared"},
	}

	_, err := resolveRouteSelectionValue(routes, "step-shared")
	if err == nil {
		t.Fatal("expected ambiguous next_step_id error")
	}
	if !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
}

func TestResolveRouteSelectionValueRejectsUnknownValue(t *testing.T) {
	routes := deterministicRoutingTestRoutes()

	_, err := resolveRouteSelectionValue(routes, "step-unknown")
	if err == nil {
		t.Fatal("expected unknown route selection error")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected mismatch error, got %v", err)
	}
}

func TestRoutingStepOwnRouteFileCandidatesIgnoresLegacyDescription(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewDefault(),
		nil,
		orchestrator.OrchestratorTypeWorkflow,
		"Workflow/demo",
		0,
		"",
		nil,
		nil,
		false,
		&orchestrator.LLMConfig{},
		1,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator returned error: %v", err)
	}
	hcpo := &StepBasedWorkflowOrchestrator{
		BaseOrchestrator:  base,
		selectedRunFolder: "run-1",
	}
	step := &RoutingPlanStep{
		CommonStepFields: CommonStepFields{
			ID:          "route-by-mode",
			Description: "legacy routing agent body",
		},
	}

	candidates := hcpo.routingStepOwnRouteFileCandidates(step, 0, "step-1")
	if len(candidates) != 1 {
		t.Fatalf("expected one own route file candidate, got %d: %v", len(candidates), candidates)
	}
	if strings.Contains(candidates[0], "step-1-routing") {
		t.Fatalf("legacy execute path should not be considered, got %q", candidates[0])
	}
}

func TestUpdateStepFromPartialCanClearRoutingDescription(t *testing.T) {
	original := &RoutingPlanStep{
		CommonStepFields: CommonStepFields{
			ID:          "route-by-mode",
			Title:       "Route by Mode",
			Description: "legacy routing agent body",
		},
		RoutingQuestion: "Which path should run?",
		Routes:          deterministicRoutingTestRoutes(),
	}

	updated, ok := mergePartialStepUpdate(original, PartialPlanStep{
		ClearDescription: true,
	}).(*RoutingPlanStep)
	if !ok {
		t.Fatalf("expected updated routing step")
	}
	if updated.Description != "" {
		t.Fatalf("expected description to be cleared, got %q", updated.Description)
	}
}

func deterministicRoutingTestRoutes() []RoutingRoute {
	return []RoutingRoute{
		{RouteID: "route-search", RouteName: "Search", NextStepID: "step-search"},
		{RouteID: "route-save", RouteName: "Save", NextStepID: "step-save"},
	}
}
