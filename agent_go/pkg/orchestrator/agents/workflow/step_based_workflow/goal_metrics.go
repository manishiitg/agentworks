package step_based_workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/goalcheck"
)

// Definitions are configured once by the builder. Measurements reuse the
// immutable Pulse observation ledger; reviewers do not maintain another report.
type GoalMetric struct {
	// Goal grouping is independent of the immutable observation criterion.
	GoalID      string   `json:"goal_id,omitempty"`
	GoalName    string   `json:"goal_name,omitempty"`
	Supports    []string `json:"supports,omitempty"`
	SupportKind string   `json:"support_kind,omitempty"`
	// A definition identifies one fixed dimension slice; its metric ID isolates
	// observations without changing legacy collection or observation identities.
	Dimensions          map[string]string `json:"dimensions,omitempty"`
	ID                  string            `json:"id"`
	CriterionID         string            `json:"criterion_id"`
	Name                string            `json:"name"`
	Role                string            `json:"role"`
	Unit                string            `json:"unit"`
	Direction           string            `json:"direction"`
	Definition          string            `json:"definition"`
	Source              string            `json:"source"`
	Window              string            `json:"window"`
	Route               string            `json:"route"`
	Environment         string            `json:"environment"`
	CollectionFrequency string            `json:"collection_frequency"`
	FreshnessHours      float64           `json:"freshness_hours"`
	Target              *float64          `json:"target,omitempty"`
	TargetDate          string            `json:"target_date,omitempty"`
}

const goalMetricsSchema = `CREATE TABLE IF NOT EXISTS workflow_goal_metrics (
 metric_id TEXT PRIMARY KEY, definition_json TEXT NOT NULL, active INTEGER NOT NULL DEFAULT 1
)`

func loadGoalMetrics(ctx context.Context, db pulseFindingLifecycleDB) ([]GoalMetric, error) {
	metrics := []GoalMetric{}
	if _, err := db.ExecContext(ctx, goalMetricsSchema); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT definition_json FROM workflow_goal_metrics WHERE active=1 ORDER BY metric_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var m GoalMetric
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, err
		}
		metrics = append(metrics, m)
	}
	return GoalMetricsWithLegacyLinks(metrics), rows.Err()
}

// ConfigureGoalMetrics atomically replaces the active selection. Omitted
// definitions are retired, never erased, so repeated setup preserves history.
func ConfigureGoalMetrics(ctx context.Context, workspacePath string, metrics []GoalMetric) error {
	if len(metrics) == 0 || len(metrics) > 30 {
		return fmt.Errorf("provide 1 to 30 metrics, including at least one primary metric")
	}
	metrics = GoalMetricsWithLegacyLinks(metrics)
	seen := map[string]bool{}
	primary := 0
	for _, m := range metrics {
		for name, v := range map[string]string{"id": m.ID, "criterion_id": m.CriterionID, "name": m.Name, "unit": m.Unit, "definition": m.Definition, "source": m.Source, "window": m.Window, "collection_frequency": m.CollectionFrequency} {
			if strings.TrimSpace(v) == "" || strings.TrimSpace(v) != v {
				return fmt.Errorf("metric %q requires a nonblank, trimmed %s", m.ID, name)
			}
		}
		if seen[m.ID] {
			return fmt.Errorf("duplicate metric id %q", m.ID)
		}
		seen[m.ID] = true
		if m.Role == "primary" {
			primary++
		} else if m.Role != "supporting" {
			return fmt.Errorf("metric role must be primary or supporting")
		}
		if m.Direction != "increase" && m.Direction != "decrease" && m.Direction != "maintain" {
			return fmt.Errorf("metric direction must be increase, decrease, or maintain")
		}
		if m.FreshnessHours <= 0 || math.IsNaN(m.FreshnessHours) || math.IsInf(m.FreshnessHours, 0) {
			return fmt.Errorf("metric %s requires positive finite freshness_hours", m.ID)
		}
		if m.Target != nil && (math.IsNaN(*m.Target) || math.IsInf(*m.Target, 0)) {
			return fmt.Errorf("target must be finite")
		}
		if m.TargetDate != "" {
			if _, err := time.Parse("2006-01-02", m.TargetDate); err != nil {
				return fmt.Errorf("target_date must be YYYY-MM-DD")
			}
			if m.Target == nil {
				return fmt.Errorf("target_date requires a target")
			}
		}
	}
	if primary == 0 {
		return fmt.Errorf("configure at least one primary metric")
	}
	if err := validateGoalMetricRelationships(metrics); err != nil {
		return err
	}
	db, err := openRunConcernsDB(ctx, workspacePath, true)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, goalMetricsSchema); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE workflow_goal_metrics SET active=0`); err != nil {
		return err
	}
	for _, m := range metrics {
		var raw string
		err = tx.QueryRowContext(ctx, `SELECT definition_json FROM workflow_goal_metrics WHERE metric_id=?`, m.ID).Scan(&raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			var old GoalMetric
			if err = json.Unmarshal([]byte(raw), &old); err != nil {
				return err
			}
			if old.CriterionID != m.CriterionID || old.Unit != m.Unit || old.Definition != m.Definition || old.Source != m.Source || old.Window != m.Window || old.Route != m.Route || old.Environment != m.Environment || old.Direction != m.Direction || !maps.Equal(old.Dimensions, m.Dimensions) {
				return fmt.Errorf("metric %q meaning changed: use a new id to keep historical series comparable", m.ID)
			}
		}
		b, _ := json.Marshal(m)
		if _, err = tx.ExecContext(ctx, `INSERT INTO workflow_goal_metrics(metric_id,definition_json,active) VALUES(?,?,1) ON CONFLICT(metric_id) DO UPDATE SET definition_json=excluded.definition_json,active=1`, m.ID, string(b)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RecordGoalObservations is callable by ordinary producing steps, without
// claiming a Pulse review run. Source run identity and evidence remain required.
func RecordGoalObservations(ctx context.Context, workspacePath string, observations []PulseGoalObservation) (*PulseImpactLedger, error) {
	ledger, err := LoadPulseImpactLedger(ctx, workspacePath, 1)
	if err != nil {
		return nil, err
	}
	definitions := map[string]GoalMetric{}
	for _, m := range ledger.Metrics {
		definitions[m.ID] = m
	}
	if len(observations) == 0 {
		return nil, fmt.Errorf("provide at least one observation")
	}
	for _, o := range observations {
		m, ok := definitions[o.Metric]
		if !ok {
			return nil, fmt.Errorf("unknown active metric %q; configure goal metrics first", o.Metric)
		}
		if o.CriterionID != m.CriterionID || o.Unit != m.Unit || o.Route != m.Route || o.Environment != m.Environment {
			return nil, fmt.Errorf("observation %q must match its criterion, unit, route and environment", o.Metric)
		}
		if len(normalizedLifecycleStrings(o.Evidence)) == 0 {
			return nil, fmt.Errorf("observation %q requires source evidence", o.Metric)
		}
		if o.Value != nil && o.Status != "" && o.Status != "ok" {
			return nil, fmt.Errorf("numeric observations must omit status or use ok; unavailable observations must omit value")
		}
		if o.Value != nil && (math.IsNaN(*o.Value) || math.IsInf(*o.Value, 0)) {
			return nil, fmt.Errorf("observation value must be finite")
		}
		if o.Value == nil && strings.TrimSpace(o.Status) == "" {
			return nil, fmt.Errorf("missing values require an explicit status")
		}
		if err := validateObservationWindow(o); err != nil {
			return nil, err
		}
	}
	db, err := openRunConcernsDB(ctx, workspacePath, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err = ensurePulseImpactSchema(ctx, db); err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, o := range observations {
		if strings.TrimSpace(o.RunID) == "" || strings.TrimSpace(o.RunID) != o.RunID {
			return nil, fmt.Errorf("run_id must be the nonblank source collection run ID")
		}
		var value sql.NullFloat64
		var stamp, unit, status, evidence, windowStart, windowEnd string
		err = tx.QueryRowContext(ctx, `SELECT value,observed_at,unit,status,evidence_json,window_start,window_end FROM pulse_goal_observations WHERE criterion_id=? AND metric=? AND run_id=? AND route=? AND environment=?`, o.CriterionID, o.Metric, o.RunID, o.Route, o.Environment).Scan(&value, &stamp, &unit, &status, &evidence, &windowStart, &windowEnd)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			sameValue := (!value.Valid && o.Value == nil) || (value.Valid && o.Value != nil && value.Float64 == *o.Value)
			if !sameValue || stamp != o.ObservedAt || unit != o.Unit || status != o.Status || evidence != pulseImpactJSON(o.Evidence) || windowStart != o.WindowStart || windowEnd != o.WindowEnd {
				return nil, fmt.Errorf("conflicting observation for metric %q and run %q; history is immutable", o.Metric, o.RunID)
			}
			continue
		}
		id := "obs-" + pulseImpactID(o.CriterionID, o.Metric, o.RunID, o.Route, o.Environment)
		_, err = tx.ExecContext(ctx, `INSERT INTO pulse_goal_observations(observation_id,criterion_id,metric,run_id,route,environment,value,status,unit,observed_at,window_start,window_end,evidence_json,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, o.CriterionID, o.Metric, o.RunID, o.Route, o.Environment, nullableFloat(o.Value), o.Status, o.Unit, o.ObservedAt, o.WindowStart, o.WindowEnd, pulseImpactJSON(o.Evidence), time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return LoadPulseImpactLedger(ctx, workspacePath, 200)
}

// GoalMetricSnapshot supplies notification writers the same facts as the UI.
type GoalMetricSnapshot struct {
	Metric             GoalMetric `json:"metric"`
	State              string     `json:"state"`
	Value              *float64   `json:"value,omitempty"`
	Change             *float64   `json:"change_from_previous,omitempty"`
	ObservedAt         string     `json:"observed_at,omitempty"`
	PreviousValue      *float64   `json:"previous_value,omitempty"`
	PreviousObservedAt string     `json:"previous_observed_at,omitempty"`
	Comparison         string     `json:"comparison"`
}

// GoalCheckInput matches DB rows to immutable metric meaning before deriving facts.
// Folder names never decide whether a measurement exists.
func GoalCheckInput(ledger *PulseImpactLedger, now time.Time) goalcheck.Input {
	in := goalcheck.Input{Now: now}
	definitions := map[string]GoalMetric{}
	for _, m := range ledger.Metrics {
		definitions[m.ID] = m
		in.Metrics = append(in.Metrics, goalcheck.Metric{ID: m.ID, Name: m.Name, Role: m.Role, Route: m.Route, Unit: m.Unit, Window: m.Window, FreshnessHours: m.FreshnessHours, CriterionID: m.CriterionID, Definition: m.Definition, Source: m.Source, Environment: m.Environment, Dimensions: m.Dimensions})
	}
	for _, o := range ledger.Observations {
		m, ok := definitions[o.Metric]
		if !ok || o.CriterionID != m.CriterionID || o.Unit != m.Unit || o.Route != m.Route || o.Environment != m.Environment {
			continue
		}
		observed, _ := time.Parse(time.RFC3339Nano, o.ObservedAt)
		recorded, _ := time.Parse(time.RFC3339Nano, o.RecordedAt)
		in.Observations = append(in.Observations, goalcheck.Observation{ID: o.ObservationID, Metric: o.Metric, RunID: o.RunID, Value: o.Value, Status: o.Status, ObservedAt: observed, RecordedAt: recorded, WindowStart: o.WindowStart, WindowEnd: o.WindowEnd, Evidence: o.Evidence})
	}
	return in
}

func validateObservationWindow(o PulseGoalObservation) error {
	observed, err := time.Parse(time.RFC3339Nano, o.ObservedAt)
	if err != nil {
		return fmt.Errorf("observed_at must be RFC3339")
	}
	if observed.After(time.Now().UTC()) {
		return fmt.Errorf("observed_at cannot be in the future")
	}
	if o.WindowStart == "" && o.WindowEnd == "" {
		return nil
	} // Legacy history stays readable.
	start, e1 := time.Parse(time.RFC3339Nano, o.WindowStart)
	end, e2 := time.Parse(time.RFC3339Nano, o.WindowEnd)
	if e1 != nil || e2 != nil || !end.After(start) || end.After(observed) {
		return fmt.Errorf("window_start and window_end must both be RFC3339, start before end, and end no later than observed_at")
	}
	return nil
}

func GoalMetricSnapshots(ledger *PulseImpactLedger, now time.Time) []GoalMetricSnapshot {
	result := []GoalMetricSnapshot{}
	in := GoalCheckInput(ledger, now)
	for i, fact := range goalcheck.Measurements(in.Metrics, in.Observations, now, goalcheck.DefaultSilenceDays*24) {
		m := ledger.Metrics[i]
		snapshot := GoalMetricSnapshot{Metric: m, State: "Measurement setup needed", Comparison: fact.Comparison}
		if fact.Latest != nil {
			snapshot.ObservedAt = fact.Latest.ObservedAt.UTC().Format(time.RFC3339Nano)
		}
		switch fact.State {
		case "unavailable":
			snapshot.State = "Measurement unavailable"
		case "measured", "stale":
			snapshot.Value = fact.Latest.Value
			snapshot.Change = fact.Change
			snapshot.State = "Baseline collecting"
			if fact.Change != nil {
				snapshot.State = "Tracking progress"
			}
			if fact.Previous != nil && goalcheck.Numeric(*fact.Previous) {
				snapshot.PreviousValue = fact.Previous.Value
				snapshot.PreviousObservedAt = fact.Previous.ObservedAt.UTC().Format(time.RFC3339Nano)
			}
			if m.Target != nil && ((m.Direction == "increase" && *snapshot.Value >= *m.Target) || (m.Direction == "decrease" && *snapshot.Value <= *m.Target) || (m.Direction == "maintain" && *snapshot.Value == *m.Target)) {
				snapshot.State = "Target met"
			}
			if fact.State == "stale" {
				snapshot.State = "Measurement stale"
			}
		}
		result = append(result, snapshot)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Metric.Role == "primary" && result[j].Metric.Role != "primary" })
	return result
}

// Legacy workflows had one primary and a flat supporting list. Resolve that
// relationship on read and reconfiguration without rewriting measurements.
func GoalMetricsWithLegacyLinks(metrics []GoalMetric) []GoalMetric {
	result := append([]GoalMetric{}, metrics...)
	primaryID := ""
	count := 0
	for _, m := range result {
		if m.Role == "primary" {
			primaryID = m.ID
			count++
		}
	}
	for i := range result {
		if result[i].Role == "supporting" && len(result[i].Supports) == 0 && count == 1 {
			result[i].Supports = []string{primaryID}
		}
	}
	return result
}

func validateGoalMetricRelationships(metrics []GoalMetric) error {
	byID := map[string]GoalMetric{}
	goalNames := map[string]string{}
	for _, m := range metrics {
		byID[m.ID] = m
	}
	for _, m := range metrics {
		if strings.TrimSpace(m.GoalID) != m.GoalID || strings.TrimSpace(m.GoalName) != m.GoalName {
			return fmt.Errorf("goal_id and goal_name must be trimmed")
		}
		if m.GoalName != "" && m.GoalID == "" {
			return fmt.Errorf("metric %q: goal_name requires goal_id", m.ID)
		}
		if m.GoalID != "" {
			if m.Role != "primary" || m.GoalName == "" {
				return fmt.Errorf("metric %q: goal_id and goal_name belong together on primary metrics; supporting measurements inherit their parents' goals", m.ID)
			}
			if name, ok := goalNames[m.GoalID]; ok && name != m.GoalName {
				return fmt.Errorf("goal %q has inconsistent names", m.GoalID)
			}
			goalNames[m.GoalID] = m.GoalName
		}
		for key, value := range m.Dimensions {
			if key == "" || value == "" || strings.TrimSpace(key) != key || strings.TrimSpace(value) != value {
				return fmt.Errorf("metric %q dimensions require nonblank trimmed keys and values", m.ID)
			}
		}
		if m.Role == "primary" {
			if len(m.Supports) > 0 || m.SupportKind != "" {
				return fmt.Errorf("primary metric %q cannot have supports or support_kind", m.ID)
			}
			continue
		}
		if len(m.Supports) == 0 {
			return fmt.Errorf("supporting metric %q requires explicit supports when multiple primary metrics are configured", m.ID)
		}
		if m.SupportKind != "" && m.SupportKind != "breakdown" && m.SupportKind != "diagnostic" && m.SupportKind != "guardrail" {
			return fmt.Errorf("metric %q support_kind must be breakdown, diagnostic or guardrail", m.ID)
		}
		seen := map[string]bool{}
		for _, id := range m.Supports {
			parent, ok := byID[id]
			if !ok || parent.Role != "primary" || seen[id] {
				return fmt.Errorf("metric %q supports must contain unique active primary metric IDs; invalid %q", m.ID, id)
			}
			seen[id] = true
			if m.SupportKind == "breakdown" && (m.Unit != parent.Unit || m.Direction != parent.Direction || m.Window != parent.Window) {
				return fmt.Errorf("breakdown %q must match primary %q unit, direction and window", m.ID, id)
			}
		}
	}
	return nil
}
