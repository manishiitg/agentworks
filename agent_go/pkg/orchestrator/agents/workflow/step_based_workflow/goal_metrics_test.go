package step_based_workflow

import (
	"context"
	"fmt"
	"testing"
	"time"
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
	o := PulseGoalObservation{CriterionID: m.CriterionID, Metric: m.ID, Unit: m.Unit, RunID: "run-1", ObservedAt: "2026-09-11T00:00:00Z", Value: pulseImpactFloat(290), Evidence: []string{"daily_metrics:2026-09-11"}}
	for i := 0; i < 2; i++ {
		if _, err := RecordGoalObservations(ctx, ws, []PulseGoalObservation{o}); err != nil {
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
	ledger, err := LoadPulseImpactLedger(ctx, ws, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Metrics) != 1 || ledger.Metrics[0].Unit != "followers" || len(ledger.Observations) != 1 {
		t.Fatalf("history/config lost: %+v", ledger)
	}
	o.Value = pulseImpactFloat(999)
	if _, err = RecordGoalObservations(ctx, ws, []PulseGoalObservation{o}); err == nil {
		t.Fatal("accepted conflicting historical value")
	}
}
func TestGoalMetricsRequirePrimaryAndComparableMeasurements(t *testing.T) {
	ctx := context.Background()
	ws := concernsWorkspace(t)
	m := testGoalMetric()
	m.Role = "supporting"
	if err := ConfigureGoalMetrics(ctx, ws, []GoalMetric{m}); err == nil {
		t.Fatal("no primary accepted")
	}
	m.Role = "primary"
	if err := ConfigureGoalMetrics(ctx, ws, []GoalMetric{m}); err != nil {
		t.Fatal(err)
	}
	o := PulseGoalObservation{CriterionID: m.CriterionID, Metric: m.ID, Unit: "percent", RunID: "run-1", ObservedAt: "2026-09-11T00:00:00Z", Value: pulseImpactFloat(5), Evidence: []string{"source"}}
	if _, err := RecordGoalObservations(ctx, ws, []PulseGoalObservation{o}); err == nil {
		t.Fatal("unit mismatch accepted")
	}
	o.Unit = m.Unit
	o.Value = nil
	o.Status = "unavailable"
	ledger, err := RecordGoalObservations(ctx, ws, []PulseGoalObservation{o})
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Observations[0].Value != nil {
		t.Fatal("missing became zero")
	}
	snapshot := GoalMetricSnapshots(ledger, time.Now())[0]
	if snapshot.Value != nil || snapshot.State != "Measurement unavailable" {
		t.Fatalf("dishonest summary: %+v", snapshot)
	}
}
func TestGoalMetricSnapshotsLatestFailureAndStaleness(t *testing.T) {
	m := testGoalMetric()
	m.Target = pulseImpactFloat(100)
	ledger := &PulseImpactLedger{Metrics: []GoalMetric{m}, Observations: []PulseGoalObservation{
		{Metric: m.ID, CriterionID: m.CriterionID, Unit: m.Unit, Value: pulseImpactFloat(290), Evidence: []string{"source"}, ObservedAt: "2026-09-09T00:00:00Z"},
		{Metric: m.ID, CriterionID: m.CriterionID, Unit: m.Unit, Status: "blocked", ObservedAt: "2026-09-10T00:00:00Z"},
	}}
	now, _ := time.Parse(time.RFC3339, "2026-09-11T12:00:00Z")
	p := GoalMetricSnapshots(ledger, now)[0]
	if p.Value != nil || p.State != "Measurement unavailable" {
		t.Fatal(p)
	}
	ledger.Observations = ledger.Observations[:1]
	p = GoalMetricSnapshots(ledger, now)[0]
	if p.State != "Measurement stale" {
		t.Fatal(p)
	}
}

func TestGoalHistoryDoesNotDisappearBehindUnrelatedPulseRows(t *testing.T) {
	ctx := context.Background()
	ws := concernsWorkspace(t)
	m := testGoalMetric()
	if err := ConfigureGoalMetrics(ctx, ws, []GoalMetric{m}); err != nil {
		t.Fatal(err)
	}
	good := PulseGoalObservation{Metric: m.ID, CriterionID: m.CriterionID, Unit: m.Unit, RunID: "goal-run", ObservedAt: "2026-09-01T00:00:00Z", Value: pulseImpactFloat(10), Evidence: []string{"source"}}
	if _, err := RecordGoalObservations(ctx, ws, []PulseGoalObservation{good}); err != nil {
		t.Fatal(err)
	}
	unrelated := []PulseGoalObservation{}
	for i := 0; i < 5; i++ {
		unrelated = append(unrelated, PulseGoalObservation{Metric: "other", CriterionID: "ops", RunID: fmt.Sprint(i), ObservedAt: "2026-09-10T00:00:00Z", Value: pulseImpactFloat(1)})
	}
	if _, err := RecordPulseImpactUpdate(ctx, ws, PulseImpactUpdate{Observations: unrelated}); err != nil {
		t.Fatal(err)
	}
	ledger, err := LoadPulseImpactLedger(ctx, ws, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(GoalMetricSnapshots(ledger, time.Now())) != 1 || GoalMetricSnapshots(ledger, time.Now())[0].Value == nil {
		t.Fatal("configured metric dropped by unrelated-row limit")
	}
	replacement := m
	replacement.ID = "followers-v2"
	replacement.Definition = "New sampling method"
	if err = ConfigureGoalMetrics(ctx, ws, []GoalMetric{replacement}); err != nil {
		t.Fatal(err)
	}
	ledger, err = LoadPulseImpactLedger(ctx, ws, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Metrics) != 1 || ledger.Metrics[0].ID != "followers-v2" || len(ledger.Observations) != 6 {
		t.Fatal("retirement erased history")
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
	ledger, err := LoadPulseImpactLedger(ctx, ws, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ledger.Metrics {
		if m.ID == diagnostic.ID && (len(m.Supports) != 1 || m.Supports[0] != voice.ID) {
			t.Fatal("legacy links not resolved")
		}
	}
	observation := PulseGoalObservation{CriterionID: diagnostic.CriterionID, Metric: diagnostic.ID, Unit: diagnostic.Unit, RunID: "run-1", Value: pulseImpactFloat(20), ObservedAt: "2026-09-12T00:00:00Z", Evidence: []string{"source"}}
	if _, err := RecordGoalObservations(ctx, ws, []PulseGoalObservation{observation}); err != nil {
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
	ledger, err = LoadPulseImpactLedger(ctx, ws, 10)
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
	ledger, err = LoadPulseImpactLedger(ctx, ws, 10)
	if err != nil || len(ledger.Observations) != 1 {
		t.Fatal("promotion lost history", err)
	}
}

func TestGoalRelationshipsRejectInvalidParentsAndSlices(t *testing.T) {
	primary := testGoalMetric()
	other := testGoalMetric()
	other.ID = "second"
	support := testGoalMetric()
	support.ID = "english"
	support.Role = "supporting"
	support.Supports = []string{primary.ID}
	support.SupportKind = "breakdown"
	support.Dimensions = map[string]string{"language": "English"}
	tests := []struct {
		name   string
		change func(*GoalMetric)
	}{
		{"unknown", func(m *GoalMetric) { m.Supports = []string{"missing"} }},
		{"self", func(m *GoalMetric) { m.Supports = []string{m.ID} }},
		{"duplicate", func(m *GoalMetric) { m.Supports = []string{primary.ID, primary.ID} }},
		{"different unit", func(m *GoalMetric) { m.Unit = "ms" }},
		{"different window", func(m *GoalMetric) { m.Window = "weekly" }},
		{"invalid kind", func(m *GoalMetric) { m.SupportKind = "outcome" }},
		{"empty dimension", func(m *GoalMetric) { m.Dimensions = map[string]string{"language": ""} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := support
			tt.change(&m)
			if err := ConfigureGoalMetrics(context.Background(), concernsWorkspace(t), []GoalMetric{primary, other, m}); err == nil {
				t.Fatal("invalid relationship accepted")
			}
		})
	}
	ctx := context.Background()
	ws := concernsWorkspace(t)
	if err := ConfigureGoalMetrics(ctx, ws, []GoalMetric{primary, other, support}); err != nil {
		t.Fatal(err)
	}
	obs := PulseGoalObservation{Metric: support.ID, CriterionID: support.CriterionID, Unit: support.Unit, RunID: "run", Value: pulseImpactFloat(10), ObservedAt: "2026-09-12T00:00:00Z", Evidence: []string{"English-only source"}}
	if _, err := RecordGoalObservations(ctx, ws, []PulseGoalObservation{obs}); err != nil {
		t.Fatal(err)
	}
	support.Dimensions = map[string]string{"language": "Hebrew"}
	if err := ConfigureGoalMetrics(ctx, ws, []GoalMetric{primary, other, support}); err == nil {
		t.Fatal("dimension identity changed")
	}
	support.ID = "hebrew"
	if err := ConfigureGoalMetrics(ctx, ws, []GoalMetric{primary, other, support}); err != nil {
		t.Fatal(err)
	}
	ledger, err := LoadPulseImpactLedger(ctx, ws, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range GoalMetricSnapshots(ledger, time.Now()) {
		if snapshot.Metric.ID == support.ID && snapshot.Value != nil {
			t.Fatal("English history leaked into Hebrew slice")
		}
	}
}
