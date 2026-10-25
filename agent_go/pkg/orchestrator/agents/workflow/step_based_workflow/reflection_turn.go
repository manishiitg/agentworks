package step_based_workflow

import (
	"context"
	"strings"
)

func LoadWorkflowDBTableNames(ctx context.Context, workspacePath string) ([]string, error) {
	db, err := openWorkflowEvidenceDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if isPlatformOwnedTable(name) {
			continue
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func isPlatformOwnedTable(name string) bool {
	switch {
	case strings.HasPrefix(name, "pulse_"):
		return true
	case strings.HasPrefix(name, "report_human_input"):
		return true
	// eval_results / eval_route_scores are retired eval tables: steps must
	// never write them, so they stay platform-owned as read-only history.
	case name == "run_concerns", name == "eval_results", name == "eval_route_scores":
		return true
	}
	return false
}
