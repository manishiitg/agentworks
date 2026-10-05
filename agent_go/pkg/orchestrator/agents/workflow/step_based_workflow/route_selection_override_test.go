package step_based_workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// A forward next_step_id jump archives the branch's preseed before it runs.
// The schedule's explicit choice must still win over a conflicting probe file,
// and the real branch executor must persist that choice and follow its target.
func TestRunRouteSelectionSurvivesArchivedPreseed(t *testing.T) {
	for _, value := range []string{"monitor", "crew-news-monitor", "unknown"} {
		t.Run(value, func(t *testing.T) {
			var routeArtifact, evaluation string
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet:
					reads++
					if strings.Contains(r.URL.Path, "/execution/router/") {
						// The explicit monitor preseed was moved to archived/run-1.
						_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "not found"})
					} else {
						// A late time-window probe wrote propose instead.
						_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "content": `{"select_route":"propose"}`})
					}
				case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/api/folders"):
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"success":true}`))
				default:
					var body struct {
						Content string `json:"content"`
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					if strings.Contains(r.URL.Path, "routing-evaluation.json") {
						evaluation = body.Content
					} else if strings.Contains(r.URL.Path, "route_selection.json") {
						routeArtifact = body.Content
					}
					_, _ = w.Write([]byte(`{"success":true}`))
				}
			}))
			defer server.Close()
			t.Setenv("WORKSPACE_API_URL", server.URL)
			base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "Workflow/demo", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			base.WorkspaceClient = workspace.NewClient(server.URL)
			base.SetWorkspacePath("Workflow/demo")
			controller := &StepBasedWorkflowOrchestrator{
				BaseOrchestrator: base, selectedRunFolder: "iteration-123-sched/default-group",
				executionOptions: &ExecutionOptions{RouteSelections: map[string]string{"router": value}},
			}
			branch := &BranchPlanStep{
				CommonStepFields: CommonStepFields{ID: "router", Title: "Route"},
				BranchQuestion:   "Which route?", RouteSourceFile: "probe/route_selection.json", DefaultRouteID: "propose",
				Routes: []RoutingRoute{{RouteID: "monitor", NextStepID: "crew-news-monitor"}, {RouteID: "propose", NextStepID: "propose-archetypes"}},
			}
			selected, _, err := controller.executeRoutingStep(context.Background(), branch, 1, &StepProgress{}, nil, 0, nil, []PlanStepInterface{branch}, nil)
			if value == "unknown" {
				if err == nil || !strings.Contains(err.Error(), "invalid route_selections") {
					t.Fatalf("invalid explicit route fell back to another path: %q, %v", selected, err)
				}
				return
			}
			if err != nil || selected != "monitor" || nextStepIDForSelectedRoute(branch, selected) != "crew-news-monitor" {
				t.Fatalf("explicit monitor route lost after cleanup: %q, %v", selected, err)
			}
			if reads != 0 {
				t.Fatalf("explicit route consulted disposable files: %d reads", reads)
			}
			for _, artifact := range []string{routeArtifact, evaluation} {
				if !strings.Contains(artifact, `"selected_route_id": "monitor"`) || !strings.Contains(artifact, `"source_kind": "route_selections"`) {
					t.Fatalf("missing truthful route-selection evidence: %s", artifact)
				}
			}
		})
	}
}
