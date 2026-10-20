package step_based_workflow

import (
	"context"
	"testing"
)

func testGoalMetric() GoalMetric {
	return GoalMetric{ID: "followers", CriterionID: "audience", Name: "Followers", Role: "primary", Unit: "followers", Direction: "increase", Definition: "Observed total followers", Source: "daily_metrics.follower_count", Window: "instant", CollectionFrequency: "daily", FreshnessHours: 48}
}
func TestGoalMetricsSetupPreservesHistoryAndRejectsSemanticChanges(t *testing.T) {
	ctx := context.Background()
	ws := concernsWorkspace(t)
	m := testGoalMetric()
	if err := ConfigureGoalMetrics(ctx, ws, []GoalMetric{m}); err != nil {
		t.Fatal(err)
	}
	o := GoalObservation{CriterionID: m.CriterionID, Metric: m.ID, Unit: m.Unit, RunID: "run-1", ObservedAt: "2026-09-11T00:00:00Z", Value: pulseImpactFloat(290), Evidence: []string{"daily_metrics:2026-09-11"}}
	for i := 0; i < 2; i++ {
		if _, err := RecordGoalObservations(ctx, ws, []GoalObservation{o}); err != nil {
			t.Fatal(err)
		}
	}
	m.Target = pulseImpactFloat(1000)
	if err := ConfigureGoalMetrics(ctx, ws, []GoalMetric{m}); err != nil {
		t.Fatal(err)
	}
	m.Unit = "percent"
	if err := ConfigureGoalMetrics(ctx, ws, []GoalMetric{m}); err == nil {
		t.Fatal("meaning changed under same ID")
	}
	ledger, err := LoadGoalMeasurements(ctx, ws, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Metrics) != 1 || ledger.Metrics[0].Unit != "followers" || len(ledger.Observations) != 1 {
		t.Fatalf("history/config lost: %+v", ledger)
	}
	o.Value = pulseImpactFloat(999)
	if _, err = RecordGoalObservations(ctx, ws, []GoalObservation{o}); err == nil {
		t.Fatal("accepted conflicting historical value")
	}
}

func TestMultiplePrimaryRelationshipsAndHistory(t *testing.T) {
	ctx := context.Background()
	ws := concernsWorkspace(t)
	voice := testGoalMetric()
	voice.ID = "voice"
	voice.GoalID = "performance"
	voice.GoalName = "Responsiveness"
	cost := testGoalMetric()
	cost.ID = "cost"
	cost.GoalID = "spend"
	cost.GoalName = "Cost control"
	diagnostic := testGoalMetric()
	diagnostic.ID = "diagnostic"
	diagnostic.Role = "supporting"
	if err := ConfigureGoalMetrics(ctx, ws, []GoalMetric{voice, diagnostic}); err != nil {
		t.Fatal(err)
	}
	ledger, err := LoadGoalMeasurements(ctx, ws, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ledger.Metrics {
		if m.ID == diagnostic.ID && (len(m.Supports) != 1 || m.Supports[0] != voice.ID) {
			t.Fatal("legacy links not resolved")
		}
	}
	observation := GoalObservation{CriterionID: diagnostic.CriterionID, Metric: diagnostic.ID, Unit: diagnostic.Unit, RunID: "run-1", Value: pulseImpactFloat(20), ObservedAt: "2026-09-12T00:00:00Z", Evidence: []string{"source"}}
	if _, err := RecordGoalObservations(ctx, ws, []GoalObservation{observation}); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureGoalMetrics(ctx, ws, []GoalMetric{voice, cost, diagnostic}); err == nil {
		t.Fatal("ambiguous supporting accepted")
	}
	diagnostic.Supports = []string{voice.ID, cost.ID}
	diagnostic.SupportKind = "diagnostic"
	if err := ConfigureGoalMetrics(ctx, ws, []GoalMetric{voice, cost, diagnostic}); err != nil {
		t.Fatal(err)
	}
	ledger, err = LoadGoalMeasurements(ctx, ws, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Metrics) != 3 || len(ledger.Observations) != 1 {
		t.Fatal("relationship changes lost history")
	}
	// Promotion of an independent outcome changes organization, not measurement meaning.
	diagnostic.Role = "primary"
	diagnostic.Supports = nil
	diagnostic.SupportKind = ""
	if err := ConfigureGoalMetrics(ctx, ws, []GoalMetric{voice, cost, diagnostic}); err != nil {
		t.Fatal(err)
	}
	ledger, err = LoadGoalMeasurements(ctx, ws, 10)
	if err != nil || len(ledger.Observations) != 1 {
		t.Fatal("promotion lost history", err)
	}
}
