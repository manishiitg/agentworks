package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/goalcheck"
)

// Exercise the real tool -> existing SQLite schema upgrade -> Pulse facts path.
func TestGoalMeasurementDBHistoryAndPulseFacts(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	ws := "Workflow/measurement"
	dir := filepath.Join(root, ws, "db")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	// Previous table format, including an original source-backed history row.
	_, err = db.Exec(`CREATE TABLE pulse_goal_observations (
 observation_id TEXT PRIMARY KEY, criterion_id TEXT NOT NULL, metric TEXT NOT NULL,
 run_id TEXT NOT NULL, route TEXT NOT NULL DEFAULT '', environment TEXT NOT NULL DEFAULT '',
 value REAL, status TEXT NOT NULL DEFAULT '', unit TEXT NOT NULL DEFAULT '',
 observed_at TEXT NOT NULL, evidence_json TEXT NOT NULL DEFAULT '[]', recorded_at TEXT NOT NULL,
 UNIQUE(criterion_id,metric,run_id,route,environment));
 INSERT INTO pulse_goal_observations(observation_id,criterion_id,metric,run_id,value,unit,observed_at,evidence_json,recorded_at)
 VALUES('old','responses','responses','source-batch-old',4,'responses','2026-09-10T00:00:00Z','["source row"]','2026-09-10T00:05:00Z')`)
	closeErr := db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	_, executors, _ := createGoalMetricTools()
	call := func(name string, args map[string]interface{}) (string, error) {
		args["workspace_path"] = ws
		return executors[name].(func(context.Context, map[string]interface{}) (string, error))(context.Background(), args)
	}
	_, err = call("configure_goal_metrics", map[string]interface{}{"metrics": []interface{}{map[string]interface{}{
		"id": "responses", "criterion_id": "responses", "name": "Qualified responses", "role": "primary", "unit": "responses", "direction": "increase",
		"definition": "Complete daily count of qualified responses", "source": "responses table", "window": "daily", "collection_frequency": "daily", "freshness_hours": 96,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	reading := func(run, stamp, start, end string, value interface{}) map[string]interface{} {
		o := map[string]interface{}{"metric": "responses", "criterion_id": "responses", "unit": "responses", "run_id": run, "observed_at": stamp, "evidence": []string{"source row"}}
		if value != nil {
			o["value"] = value
		} else {
			o["status"] = "unavailable"
		}
		if start != "" {
			o["window_start"] = start
		}
		if end != "" {
			o["window_end"] = end
		}
		return o
	}
	record := func(o map[string]interface{}) error {
		_, err := call("record_goal_observations", map[string]interface{}{"observations": []interface{}{o}})
		return err
	}
	viewAt := func(stamp string) *GoalStatusView {
		now, err := time.Parse(time.RFC3339, stamp)
		if err != nil {
			t.Fatal(err)
		}
		v, err := computeGoalStatus(context.Background(), ws, now)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	first := reading("source-batch-1", "2026-09-11T00:00:00Z", "2026-09-10T00:00:00Z", "2026-09-11T00:00:00Z", 0)
	if err = record(first); err != nil {
		t.Fatal(err)
	}
	fact := viewAt("2026-09-14T01:00:00Z").Facts.Measurements[0]
	if fact.State != "measured" || fact.Latest.Value == nil || *fact.Latest.Value != 0 || fact.Change != nil || len(fact.History) != 2 || fact.History[0].WindowStart != "" {
		t.Fatalf("folder name, default freshness or unknown period corrupted DB facts: %+v", fact)
	}
	if stale := viewAt("2026-09-15T01:00:00Z").Facts.Measurements[0]; stale.State != "stale" {
		t.Fatal(stale)
	}
	second := reading("source-batch-2", "2026-09-12T00:00:00Z", "2026-09-11T00:00:00Z", "2026-09-12T00:00:00Z", 4)
	for i := 0; i < 2; i++ {
		if err = record(second); err != nil {
			t.Fatal(err)
		}
	}
	v := viewAt("2026-09-12T01:00:00Z")
	fact = v.Facts.Measurements[0]
	if v.Facts.Status == goalcheck.StatusNotMeasured || fact.State != "measured" || fact.Change == nil || *fact.Change != 4 || fact.Previous.Value == nil || *fact.Previous.Value != 0 || len(fact.History) != 3 || v.Facts.LastRunMeasuredAt != "" {
		t.Fatalf("DB comparison depended on a folder or lost zero/history: %+v", v)
	}
	second["window_start"] = "2026-09-10T00:00:00Z"
	if record(second) == nil {
		t.Fatal("accepted conflicting historical period")
	}
	bad := reading("bad-window", "2026-09-12T00:00:00Z", "2026-09-11T00:00:00Z", "", 4)
	if record(bad) == nil {
		t.Fatal("accepted incomplete period")
	}
	bad = reading("future", time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "", "", 999)
	if record(bad) == nil {
		t.Fatal("accepted future evidence")
	}
	if err = record(reading("source-batch-3", "2026-09-13T00:00:00Z", "", "", nil)); err != nil {
		t.Fatal(err)
	}
	v = viewAt("2026-09-13T01:00:00Z")
	if v.Facts.Status != goalcheck.StatusNotMeasured || v.Facts.Measurements[0].State != "unavailable" || v.Facts.Measurements[0].Change != nil {
		t.Fatal(v)
	}
	if err = record(reading("source-batch-4", "2026-09-14T00:00:00Z", "2026-09-13T00:00:00Z", "2026-09-14T00:00:00Z", 6)); err != nil {
		t.Fatal(err)
	}
	if fact = viewAt("2026-09-14T01:00:00Z").Facts.Measurements[0]; fact.Change != nil || len(fact.History) != 5 {
		t.Fatalf("skipped an unavailable baseline: %+v", fact)
	}
	if raw, err := call("get_goal_metrics", map[string]interface{}{}); err != nil || !json.Valid([]byte(raw)) {
		t.Fatalf("history tool: %s / %v", raw, err)
	}
}

func TestGoalMetricToolsRoundTripMultipleOutcomes(t *testing.T) {
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
		Progress []json.RawMessage
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Metrics) != 3 || len(result.Progress) != 3 {
		t.Fatal("tool dropped outcomes", raw)
	}
}
