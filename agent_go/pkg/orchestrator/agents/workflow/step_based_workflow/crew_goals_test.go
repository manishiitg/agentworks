package step_based_workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-746-265: adopting a deliberately copied legacy ledger must preserve
// observation identities/times and business tables, not recreate measurements.
func TestCrewGoalAdoptsCopiedLedgerWithoutRewritingHistory(t *testing.T) {
	base := t.TempDir()
	base, _ = filepath.EvalSymlinks(base)
	docs, state := filepath.Join(base, "docs"), filepath.Join(base, "state")
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	if err := os.MkdirAll(filepath.Join(docs, "Workflow/legacy"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source := "Workflow/legacy"
	metric := testGoalMetric()
	if err := ConfigureGoalMetrics(ctx, source, []GoalMetric{metric}); err != nil {
		t.Fatal(err)
	}
	before, err := RecordGoalObservations(ctx, source, []GoalObservation{{Metric: metric.ID, CriterionID: metric.CriterionID, Unit: metric.Unit, RunID: "legacy-run", ObservedAt: "2026-09-11T00:00:00Z", Value: goalMetricValue(12), Evidence: []string{"source report"}}})
	if err != nil {
		t.Fatal(err)
	}
	private, err := crewroot.PrivateRoot(docs, "crew")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(docs, "Crew/example"), filepath.Join(private, "db"), filepath.Join(state, "ownership")} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(map[string]any{"projects": map[string]crewroot.Record{"work/example": {Product: "work", Folder: "example", ID: "crew", Owner: "owner"}}})
	if err := os.WriteFile(filepath.Join(state, "ownership/projects.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := openWorkflowEvidenceDB(ctx, source, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE business_data (value TEXT); INSERT INTO business_data VALUES ('keep')`); err != nil {
		t.Fatal(err)
	}
	// A consistent SQLite copy into an empty isolated target; no merging of
	// unrelated databases and no live installation is modified by this test.
	if _, err := db.Exec(`VACUUM INTO ?`, filepath.Join(private, "db/db.sqlite")); err != nil {
		t.Fatal(err)
	}
	db.Close()
	goal := CrewGoal{ID: "legacy", Name: "Audience", Status: "active"}
	if err := SaveCrewGoal(ctx, "Crew/example", goal, []GoalMetric{metric}); err != nil {
		t.Fatal(err)
	}
	after, err := LoadGoalMeasurements(ctx, "Crew/example", 10)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(before.Observations)
	b, _ := json.Marshal(after.Observations)
	if string(a) != string(b) {
		t.Fatal("copied history changed", string(a), string(b))
	}
	target, err := openWorkflowEvidenceDB(ctx, "Crew/example", false)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	var business string
	if err := target.QueryRow(`SELECT value FROM business_data`).Scan(&business); err != nil || business != "keep" {
		t.Fatal("business data changed", err)
	}
	sourceLedger, err := LoadGoalMeasurements(ctx, source, 10)
	if err != nil || sourceLedger.Metrics[0].GoalID != "" {
		t.Fatal("source ledger was mutated", err)
	}
}
