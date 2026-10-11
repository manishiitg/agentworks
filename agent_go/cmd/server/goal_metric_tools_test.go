package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/goalcheck"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// PLAT-822: real metric tools and run recording preserve DB/run evidence without
// interpreting scope labels, or even an equal route name, as goal contribution.
func TestGoalRunEvidenceDoesNotRequireMetricRouteMappings(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	ws := "Workflow/run-evidence"
	workspace := &mockWorkspaceAPI{files: map[string]string{ws + "/workflow.json": `{"id":"run-evidence","label":"Run evidence","pulse":{"enabled":true}}`}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	_, executors, _ := createGoalMetricTools()
	call := func(name string, args map[string]interface{}) string {
		t.Helper()
		args["workspace_path"] = ws
		out, err := executors[name].(func(context.Context, map[string]interface{}) (string, error))(ctx, args)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return out
	}
	metrics := []interface{}{}
	observations := []interface{}{}
	now := time.Now().UTC().Truncate(time.Second)
	for i, scope := range []string{"", "all", "segment-a,segment-b", "region-*", "delivery"} {
		id := []string{"unscoped", "whole", "segments", "region", "matching-label"}[i]
		metrics = append(metrics, map[string]interface{}{"id": id, "criterion_id": id, "name": id, "role": "primary", "unit": "count", "direction": "increase", "definition": "Source-backed response count", "source": "response rows", "window": "snapshot", "collection_frequency": "daily", "freshness_hours": 96, "route": scope})
		observations = append(observations, map[string]interface{}{"metric": id, "criterion_id": id, "unit": "count", "route": scope, "run_id": "source-batch-" + id, "observed_at": now.Add(-time.Minute).Format(time.RFC3339), "value": 4, "evidence": []string{"source row"}})
	}
	call("configure_goal_metrics", map[string]interface{}{"metrics": metrics})
	call("record_goal_observations", map[string]interface{}{"observations": observations})
	before := call("get_goal_metrics", map[string]interface{}{})
	// Existing DBs retain old history, even if they contain a retired derived
	// column. The new recorder does not read/write that classification.
	db, err := sql.Open("sqlite", filepath.Join(root, ws, "db", "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE goal_run_facts (
 run_folder TEXT NOT NULL, finished_at TEXT NOT NULL, started_at TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL, routes_json TEXT NOT NULL DEFAULT '[]', goal_work_ran INTEGER NOT NULL DEFAULT 0,
 goal_measured INTEGER NOT NULL DEFAULT 0, recorded_at TEXT NOT NULL, PRIMARY KEY(run_folder,finished_at))`)
	if err == nil {
		oldAt := now.Add(-goalCheckHistoryWindow - time.Hour).Format(time.RFC3339)
		_, err = db.Exec(`INSERT INTO goal_run_facts VALUES('iteration-older-sched/default',?,?,'completed','[]',1,0,?)`, oldAt, oldAt, oldAt)
	}
	closeErr := db.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("old run store: %v %v", err, closeErr)
	}
	for i, run := range []string{"iteration-1-sched/default", "iteration-2-sched/default"} {
		if i == 1 {
			dir := filepath.Join(root, ws, "runs", run, "execution", "routing")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "route_selection.json"), []byte(`{"selected_route_id":"delivery"}`), 0644); err != nil {
				t.Fatal(err)
			}
		}
		if err := stepworkflow.RecordGoalRunFacts(ctx, ws, run, "completed", now.Add(-time.Hour), now.Add(time.Duration(i-2)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	view, err := computeGoalStatus(ctx, ws, now)
	if err != nil {
		t.Fatal(err)
	}
	if view.Facts.Status != goalcheck.StatusOK || len(view.Facts.Alarms) != 0 || len(view.Facts.RecentRuns) != 2 || len(view.Facts.Measurements) != 5 || len(view.Facts.RecentRuns[0].Routes) != 1 || view.Facts.RecentRuns[0].Routes[0] != "delivery" || view.Facts.RecentRuns[0].GoalMeasured {
		t.Fatalf("scope labels or separate producers corrupted measurement/execution evidence: %+v", view.Facts)
	}
	encoded, _ := json.Marshal(view)
	for _, retired := range []string{"goal_work_ran", "goal_routes", "days_since_goal_work", "last_goal_work_at", "goal_work_skipped", "goal_work_not_measuring", "its work is running"} {
		if strings.Contains(string(encoded), retired) {
			t.Fatalf("derived goal-work claim still exposed: %s", retired)
		}
	}
	if after := call("get_goal_metrics", map[string]interface{}{}); after != before {
		t.Fatal("execution recording changed measurement definitions/history")
	}
	if runs, err := stepworkflow.LoadGoalRunFacts(ctx, ws, now.AddDate(-1, 0, 0)); err != nil || len(runs) != 3 {
		t.Fatalf("old execution history lost: %+v %v", runs, err)
	}
	stale, err := computeGoalStatus(ctx, ws, now.Add(5*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{goalcheck.AlarmMeasurementStale: false, goalcheck.AlarmNoRun: false}
	for _, alarm := range stale.Facts.Alarms {
		if _, ok := want[alarm.Kind]; !ok {
			t.Fatalf("unexpected alarm after real evidence gaps: %+v", alarm)
		}
		want[alarm.Kind] = true
	}
	if !want[goalcheck.AlarmMeasurementStale] || !want[goalcheck.AlarmNoRun] {
		t.Fatalf("genuine measurement/run silence disappeared: %+v", stale.Facts)
	}
	query := pulseLifecycleGoalCheckStep(ctx, ws, "run-evidence-check", workflowNotificationContentInstructions{}).query
	if !strings.Contains(query, "No metric-to-route declaration is required") || !strings.Contains(query, `"recent_runs"`) {
		t.Fatal("Pulse goal turn lacks evidence and agentic judgment guidance")
	}
}

// Exercise the real tool -> existing SQLite schema upgrade -> Pulse facts path.
func TestGoalMeasurementDBHistoryAndPulseFacts(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspace := &mockWorkspaceAPI{files: map[string]string{}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
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
	setPulse := func(enabled bool) {
		raw, err := json.Marshal(map[string]interface{}{"id": "measurement", "label": "Measurement", "pulse": map[string]interface{}{"enabled": enabled}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ws, "workflow.json"), raw, 0644); err != nil {
			t.Fatal(err)
		}
		workspace.mu.Lock()
		workspace.files[ws+"/workflow.json"] = string(raw)
		workspace.mu.Unlock()
		manifest, found, err := ReadWorkflowManifest(context.Background(), ws)
		if err != nil || !found || manifest.PulseEnabled() != enabled {
			t.Fatalf("manifest toggle: found=%v err=%v manifest=%+v", found, err, manifest)
		}
	}
	setPulse(false)
	if viewAt("2026-09-10T01:00:00Z").MeasurementUpgrade != nil {
		t.Fatal("disabled Pulse requested a measurement migration")
	}
	setPulse(true)
	if upgrade := viewAt("2026-09-10T01:00:00Z").MeasurementUpgrade; upgrade == nil || !upgrade.Required {
		t.Fatal("enabling Pulse failed to detect the old period format")
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
	if v.MeasurementUpgrade == nil || v.MeasurementUpgrade.Required {
		t.Fatal("verified current period kept requesting migration", v.MeasurementUpgrade)
	}
	setPulse(false)
	if viewAt("2026-09-12T01:00:00Z").MeasurementUpgrade != nil {
		t.Fatal("disabled workflow kept migration work")
	}
	setPulse(true)
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
