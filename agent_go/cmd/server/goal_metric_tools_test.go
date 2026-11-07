package server

import (
	"context"
	"encoding/json"
	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// PLAT-822: real metric tools and run recording preserve DB/run evidence without
// interpreting scope labels, or even an equal route name, as goal contribution.

// Exercise the real tool -> existing SQLite schema -> Goal measurements response.

func TestGoalMetricToolsRoundTripMultipleOutcomes(t *testing.T) {
	env := newCrewFunctionEnv(t)
	env.mock.mu.Lock()
	env.mock.files["Workflow/metrics/workflow.json"] = `{"id":"metrics","access":{"owners":["owner"]}}`
	env.mock.mu.Unlock()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	ws := "Workflow/metrics"
	if err := os.MkdirAll(filepath.Join(root, ws, "db"), 0755); err != nil {
		t.Fatal(err)
	}
	_, executors, _ := createGoalMetricTools()
	call := func(name string, args map[string]interface{}) (string, error) {
		args["workspace_path"] = ws
		return executors[name].(func(context.Context, map[string]interface{}) (string, error))(context.Background(), args)
	}
	metric := func(id, goal string) map[string]interface{} {
		return map[string]interface{}{"id": id, "criterion_id": id, "name": id, "role": "primary", "goal_id": goal, "goal_name": goal, "unit": "ms", "direction": "decrease", "definition": "p50", "source": "samples", "window": "daily", "collection_frequency": "daily", "freshness_hours": 48}
	}
	latency := metric("voice", "performance")
	cost := metric("cost", "spend")
	english := metric("English", "performance")
	english["role"] = "supporting"
	delete(english, "goal_id")
	delete(english, "goal_name")
	english["supports"] = []string{"voice"}
	english["support_kind"] = "breakdown"
	english["dimensions"] = map[string]string{"language": "English"}
	if _, err := call("configure_goal_metrics", map[string]interface{}{"metrics": []interface{}{latency, cost, english}}); err != nil {
		t.Fatal(err)
	}
	if _, err := call("record_goal_observations", map[string]interface{}{"observations": []interface{}{map[string]interface{}{"metric": "voice", "criterion_id": "voice", "unit": "ms", "run_id": "run-1", "observed_at": "2026-09-12T00:00:00Z", "value": 120, "evidence": []string{"source"}}}}); err != nil {
		t.Fatal(err)
	}
	raw, err := call("get_goal_metrics", map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Metrics []struct {
			ID       string   `json:"id"`
			Supports []string `json:"supports"`
		}
		Observations []json.RawMessage
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Metrics) != 3 || len(result.Observations) != 1 {
		t.Fatal("tool dropped outcomes", raw)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/workflow/goal-metrics?workspace_path="+ws, nil)
	(&StreamingAPI{}).handleGetGoalMetrics(response, request)
	var measurements step.GoalMeasurementLedger
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &measurements) != nil || len(measurements.Metrics) != 3 || len(measurements.Observations) != 1 {
		t.Fatalf("Goal measurements response lost saved values: %d %s", response.Code, response.Body.String())
	}
}
