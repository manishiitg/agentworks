package step_based_workflow

import (
	"context"
	"encoding/json"
)

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

func loadGoalMetrics(ctx context.Context, db workflowEvidenceDB) ([]GoalMetric, error) {
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

// Legacy workflows had one primary and a flat supporting list. Resolve that
// relationship on read without rewriting measurements.
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
