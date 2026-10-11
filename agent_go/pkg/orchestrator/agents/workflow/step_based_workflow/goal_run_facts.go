package step_based_workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/goalcheck"
)

// After-run goal facts (PLAT-697 phase 1, code only): when a workflow run with
// a configured goal finishes, record its actual route selections and whether
// the run recorded a primary goal reading. Goal contribution is agent judgment. Run
// folders rotate away; this keeps the silence alarm's history. The readings
// themselves stay in pulse_goal_observations.
const goalRunFactsSchema = `CREATE TABLE IF NOT EXISTS goal_run_facts (
	run_folder TEXT NOT NULL,
	finished_at TEXT NOT NULL,
	started_at TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	routes_json TEXT NOT NULL DEFAULT '[]',
	goal_measured INTEGER NOT NULL DEFAULT 0,
	recorded_at TEXT NOT NULL,
	PRIMARY KEY (run_folder, finished_at)
)`

// RunRouteSelections reads the routes a finished run took from its routing
// steps' route_selection.json files.
func RunRouteSelections(workspacePath, runFolder string) []string {
	root := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(strings.Trim(strings.TrimSpace(workspacePath), "/")), "runs", filepath.FromSlash(strings.Trim(runFolder, "/")))
	matches, _ := filepath.Glob(filepath.Join(root, "execution", "*", "route_selection.json"))
	seen := map[string]bool{}
	routes := []string{}
	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var sel struct {
			SelectedRouteID string `json:"selected_route_id"`
			SelectRoute     string `json:"select_route"`
		}
		if json.Unmarshal(raw, &sel) != nil {
			continue
		}
		route := strings.TrimSpace(sel.SelectedRouteID)
		if route == "" {
			route = strings.TrimSpace(sel.SelectRoute)
		}
		if route != "" && !seen[route] {
			seen[route] = true
			routes = append(routes, route)
		}
	}
	sort.Strings(routes)
	return routes
}

// RecordGoalRunFacts stores one finished run's goal facts. A workflow without
// a primary goal metric records nothing. Errors never affect the run.
func RecordGoalRunFacts(ctx context.Context, workspacePath, runFolder, status string, startedAt, finishedAt time.Time) error {
	if strings.TrimSpace(runFolder) == "" {
		return nil
	}
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return err
	}
	defer db.Close()
	metrics, err := loadGoalMetrics(ctx, db)
	if err != nil {
		return err
	}
	primary := []interface{}{}
	for _, m := range metrics {
		if m.Role == "primary" {
			primary = append(primary, m.ID)
		}
	}
	if len(primary) == 0 {
		return nil
	}
	if _, err := db.ExecContext(ctx, goalRunFactsSchema); err != nil {
		return err
	}
	routes := RunRouteSelections(workspacePath, runFolder)
	// Measured: a primary reading from this run's folder recorded while it ran.
	top := strings.Trim(runFolder, "/")
	if i := strings.Index(top, "/"); i >= 0 {
		top = top[:i]
	}
	args := append([]interface{}{top, top + "/%", startedAt.UTC().Add(-time.Minute).Format(time.RFC3339Nano)}, primary...)
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(primary)), ",")
	var measured int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pulse_goal_observations WHERE (run_id=? OR run_id LIKE ?) AND recorded_at>=? AND value IS NOT NULL AND metric IN (`+placeholders+`)`, args...).Scan(&measured); err != nil {
		measured = 0
	}
	routesJSON, _ := json.Marshal(routes)
	_, err = db.ExecContext(ctx, `INSERT OR REPLACE INTO goal_run_facts (run_folder,finished_at,started_at,status,routes_json,goal_measured,recorded_at) VALUES (?,?,?,?,?,?,?)`,
		strings.Trim(runFolder, "/"), finishedAt.UTC().Format(time.RFC3339Nano), startedAt.UTC().Format(time.RFC3339Nano), status, string(routesJSON),
		boolInt(measured > 0), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// LoadGoalRunFacts returns recorded runs finished after since, newest first.
func LoadGoalRunFacts(ctx context.Context, workspacePath string, since time.Time) ([]goalcheck.Run, error) {
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, goalRunFactsSchema); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT run_folder,finished_at,started_at,status,routes_json,goal_measured FROM goal_run_facts WHERE finished_at>=? ORDER BY finished_at DESC LIMIT 500`, since.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []goalcheck.Run{}
	for rows.Next() {
		var folder, finished, started, status, routesJSON string
		var measured int
		if err := rows.Scan(&folder, &finished, &started, &status, &routesJSON, &measured); err != nil {
			return nil, err
		}
		run := goalcheck.Run{RunID: folder, Status: status, Measured: measured == 1}
		run.FinishedAt, _ = time.Parse(time.RFC3339Nano, finished)
		run.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
		_ = json.Unmarshal([]byte(routesJSON), &run.Routes)
		runs = append(runs, run)
	}
	return runs, rows.Err()
}
