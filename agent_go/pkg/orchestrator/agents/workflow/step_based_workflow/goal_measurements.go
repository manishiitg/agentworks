package step_based_workflow

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
)

const goalObservationsSchema = `CREATE TABLE IF NOT EXISTS goal_observations (
	observation_id TEXT PRIMARY KEY,
	criterion_id TEXT NOT NULL,
	metric TEXT NOT NULL,
	run_id TEXT NOT NULL,
	route TEXT NOT NULL DEFAULT '',
	environment TEXT NOT NULL DEFAULT '',
	value REAL,
	status TEXT NOT NULL DEFAULT '',
	unit TEXT NOT NULL DEFAULT '',
	observed_at TEXT NOT NULL,
	window_start TEXT NOT NULL DEFAULT '',
	window_end TEXT NOT NULL DEFAULT '',
	evidence_json TEXT NOT NULL DEFAULT '[]',
	recorded_at TEXT NOT NULL,
	UNIQUE (criterion_id, metric, run_id, route, environment)
)`

type GoalObservation struct {
	ObservationID string   `json:"observation_id"`
	CriterionID   string   `json:"criterion_id"`
	Metric        string   `json:"metric"`
	RunID         string   `json:"run_id"`
	Route         string   `json:"route,omitempty"`
	Environment   string   `json:"environment,omitempty"`
	Value         *float64 `json:"value,omitempty"`
	Status        string   `json:"status,omitempty"`
	Unit          string   `json:"unit,omitempty"`
	ObservedAt    string   `json:"observed_at"`
	WindowStart   string   `json:"window_start,omitempty"`
	WindowEnd     string   `json:"window_end,omitempty"`
	Evidence      []string `json:"evidence,omitempty"`
	RecordedAt    string   `json:"recorded_at,omitempty"`
}

type GoalMeasurementLedger struct {
	Metrics      []GoalMetric      `json:"metrics"`
	Observations []GoalObservation `json:"observations"`
}

func ensureGoalMeasurementSchema(ctx context.Context, db workflowEvidenceDB) error {
	for _, ddl := range []string{goalMetricsSchema, goalObservationsSchema} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	return nil
}

func goalObservationID(parts ...string) string {
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		normalized = append(normalized, strings.TrimSpace(part))
	}
	sum := sha256.Sum256([]byte(strings.Join(normalized, "\x00")))
	return hex.EncodeToString(sum[:])[:20]
}

func goalEvidenceJSON(values []string) string {
	encoded, _ := json.Marshal(normalizedEvidenceStrings(values))
	return string(encoded)
}

func nullableFloat(value *float64) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func decodeGoalEvidence(raw string) []string {
	var values []string
	_ = json.Unmarshal([]byte(raw), &values)
	if values == nil {
		return []string{}
	}
	return values
}

func LoadGoalMeasurements(ctx context.Context, workspacePath string, limit int) (*GoalMeasurementLedger, error) {
	if limit <= 0 {
		limit = 200
	}
	ledger := &GoalMeasurementLedger{Metrics: []GoalMetric{}, Observations: []GoalObservation{}}
	db, err := openWorkflowEvidenceDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return ledger, err
	}
	defer db.Close()
	if err = ensureGoalMeasurementSchema(ctx, db); err != nil {
		return nil, err
	}
	ledger.Metrics, err = loadGoalMetrics(ctx, db)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT observation_id, criterion_id, metric, run_id, route, environment,
		value, status, unit, observed_at, window_start, window_end, evidence_json, recorded_at
		FROM goal_observations WHERE observation_id IN (
 SELECT observation_id FROM goal_observations ORDER BY julianday(observed_at) DESC, recorded_at DESC LIMIT ?
 ) OR observation_id IN (
 SELECT observation_id FROM (
 SELECT o.observation_id, ROW_NUMBER() OVER (PARTITION BY o.metric,o.criterion_id,o.unit,o.route,o.environment ORDER BY julianday(o.observed_at) DESC, o.recorded_at DESC) AS position
 FROM goal_observations o JOIN workflow_goal_metrics m ON m.metric_id=o.metric AND m.active=1
 WHERE o.criterion_id=json_extract(m.definition_json,'$.criterion_id') AND o.unit=json_extract(m.definition_json,'$.unit')
 AND o.route=json_extract(m.definition_json,'$.route') AND o.environment=json_extract(m.definition_json,'$.environment')
 ) WHERE position<=120
 ) ORDER BY julianday(observed_at) DESC, recorded_at DESC`, limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item GoalObservation
		var value sql.NullFloat64
		var evidenceJSON string
		if err := rows.Scan(&item.ObservationID, &item.CriterionID, &item.Metric, &item.RunID, &item.Route,
			&item.Environment, &value, &item.Status, &item.Unit, &item.ObservedAt, &item.WindowStart, &item.WindowEnd, &evidenceJSON, &item.RecordedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if value.Valid {
			v := value.Float64
			item.Value = &v
		}
		item.Evidence = decodeGoalEvidence(evidenceJSON)
		ledger.Observations = append(ledger.Observations, item)
	}
	rows.Close()

	return ledger, nil
}
