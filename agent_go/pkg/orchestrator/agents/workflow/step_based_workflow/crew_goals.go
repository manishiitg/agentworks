package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Crew goals describe outcomes, not another kind of executable. Their readings
// use the existing measurement ledger in the parent's private database.
type CrewGoal struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Outcome     string   `json:"outcome"`
	Status      string   `json:"status"`
	WorkflowIDs []string `json:"workflow_ids"`
	MetricIDs   []string `json:"metric_ids"`
	UpdatedAt   string   `json:"updated_at"`
}

const crewGoalsSchema = `CREATE TABLE IF NOT EXISTS crew_goals (id TEXT PRIMARY KEY, definition_json TEXT NOT NULL)`

// Existing measurement readers use the workflow database, never its Crew database.
// The remaining metric API is retired separately after the data cutover.
func GoalMeasurementWorkspace(root string) (string, error) {
	layout, found, err := crewroot.LookupExecutable(fsutil.WorkspaceDocsRoot(), root)
	if err != nil {
		return "", err
	}
	if found {
		if root != layout.Binding.Root {
			return "", fmt.Errorf("use the workflow's root for goal measurements")
		}
		return layout.Binding.Root, nil
	}
	ref, valid := workspaceref.Parse(root)
	if !valid || !strings.HasPrefix(ref.Logical(), "Workflow/") {
		return "", fmt.Errorf("use the workflow database for progress; open its dashboard to view it")
	}
	return root, nil
}

func loadCrewGoals(ctx context.Context, db workflowEvidenceDB) ([]CrewGoal, error) {
	goals := []CrewGoal{}
	if _, err := db.ExecContext(ctx, crewGoalsSchema); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT definition_json FROM crew_goals ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var goal CrewGoal
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &goal); err != nil {
			return nil, err
		}
		goals = append(goals, goal)
	}
	return goals, rows.Err()
}

func LoadCrewGoals(ctx context.Context, root string) ([]CrewGoal, error) {
	db, err := openWorkflowEvidenceDB(ctx, root, false)
	if err != nil || db == nil {
		return []CrewGoal{}, err
	}
	defer db.Close()
	return loadCrewGoals(ctx, db)
}

// SaveCrewGoal updates one outcome and its measurements atomically. Replacing
// one goal cannot retire another goal's metrics or rewrite its observation IDs.
func SaveCrewGoal(ctx context.Context, root string, goal CrewGoal, metrics []GoalMetric) error {
	roots, found, err := crewroot.Lookup(fsutil.WorkspaceDocsRoot(), root)
	if err != nil {
		return err
	}
	if !found || roots.Virtual != root {
		return fmt.Errorf("choose a registered Crew")
	}
	if strings.TrimSpace(goal.ID) == "" || len(goal.ID) > 100 || strings.ContainsAny(goal.ID, "/\\") || goal.ID != strings.TrimSpace(goal.ID) {
		return fmt.Errorf("use a stable goal ID")
	}
	goal.Name = strings.TrimSpace(goal.Name)
	goal.Outcome = strings.TrimSpace(goal.Outcome)
	if goal.Name == "" || len(goal.Name) > 160 || len(goal.Outcome) > 4000 {
		return fmt.Errorf("enter a goal name up to 160 characters and an outcome up to 4000 characters")
	}
	if goal.Status == "" {
		goal.Status = "active"
	}
	if goal.Status != "active" && goal.Status != "achieved" && goal.Status != "stopped" {
		return fmt.Errorf("choose active, achieved or stopped")
	}
	goal.WorkflowIDs = normalizedEvidenceStrings(goal.WorkflowIDs)
	goal.MetricIDs = []string{}
	for i := range metrics {
		if metrics[i].Role == "primary" {
			metrics[i].GoalID = goal.ID
			metrics[i].GoalName = goal.Name
		}
		goal.MetricIDs = append(goal.MetricIDs, metrics[i].ID)
	}
	if len(metrics) > 0 {
		if err := validateGoalMetrics(metrics); err != nil {
			return err
		}
	}
	db, err := openWorkflowEvidenceDB(ctx, root, true)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensureGoalMeasurementSchema(ctx, db); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, crewGoalsSchema); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	goals, err := loadCrewGoals(ctx, tx)
	if err != nil {
		return err
	}
	oldIDs := []string{}
	for _, existing := range goals {
		if existing.ID == goal.ID {
			oldIDs = existing.MetricIDs
			continue
		}
		for _, id := range goal.MetricIDs {
			if slices.Contains(existing.MetricIDs, id) {
				return fmt.Errorf("measurement %s belongs to another goal; choose a new ID", id)
			}
		}
	}
	current, err := loadGoalMetrics(ctx, tx)
	if err != nil {
		return err
	}
	combined := append([]GoalMetric{}, metrics...)
	for _, m := range current {
		if !slices.Contains(oldIDs, m.ID) && !slices.Contains(goal.MetricIDs, m.ID) {
			combined = append(combined, m)
		}
	}
	if len(combined) > 0 {
		if err := validateGoalMetrics(combined); err != nil {
			return err
		}
	}
	if err := saveGoalMetricDefinitions(ctx, tx, combined); err != nil {
		return err
	}
	goal.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(goal)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO crew_goals(id,definition_json) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET definition_json=excluded.definition_json`, goal.ID, string(data)); err != nil {
		return err
	}
	return tx.Commit()
}

// No execution callback changes a goal's status. Target facts are derived from
// fresh, matching evidence; a successful run alone is never goal progress.
type CrewGoalProgress struct {
	State      string   `json:"state"`
	TargetsMet bool     `json:"targets_met"`
	Current    *float64 `json:"current,omitempty"`
	Unit       string   `json:"unit,omitempty"`
}

func SummarizeCrewGoal(goal CrewGoal, ledger *GoalMeasurementLedger, now time.Time) CrewGoalProgress {
	result := CrewGoalProgress{State: "No measurements"}
	targets, met, missing, stale := 0, 0, 0, 0
	primaries := 0
	for _, m := range ledger.Metrics {
		if !slices.Contains(goal.MetricIDs, m.ID) || m.Role != "primary" {
			continue
		}
		primaries++
		var latest *GoalObservation
		var latestTime time.Time
		for i := range ledger.Observations {
			o := &ledger.Observations[i]
			at, err := time.Parse(time.RFC3339Nano, o.ObservedAt)
			if err != nil || at.After(now) || o.Metric != m.ID || o.CriterionID != m.CriterionID || o.Unit != m.Unit || o.Route != m.Route || o.Environment != m.Environment {
				continue
			}
			if latest == nil || at.After(latestTime) || (at.Equal(latestTime) && o.RecordedAt > latest.RecordedAt) {
				latest = o
				latestTime = at
			}
		}
		if m.Target != nil {
			targets++
		}
		if latest == nil || latest.Value == nil || len(normalizedEvidenceStrings(latest.Evidence)) == 0 {
			missing++
			continue
		}
		if primaries == 1 {
			result.Current = latest.Value
			result.Unit = m.Unit
		}
		if now.Sub(latestTime).Hours() > m.FreshnessHours {
			stale++
			continue
		}
		if latest.Status != "" && latest.Status != "ok" {
			missing++
			continue
		}
		if m.Target != nil && ((m.Direction == "increase" && *latest.Value >= *m.Target) || (m.Direction == "decrease" && *latest.Value <= *m.Target) || (m.Direction == "maintain" && *latest.Value == *m.Target)) {
			met++
		}
	}
	if primaries > 1 {
		result.Current = nil
		result.Unit = ""
	}
	switch {
	case primaries == 0:
	case missing > 0:
		result.State = "Measurement needed"
	case stale > 0:
		result.State = "Needs fresh measurements"
	case targets > 0 && met == targets:
		result.State = "Targets met"
		result.TargetsMet = true
	default:
		result.State = "Tracking"
	}
	return result
}
