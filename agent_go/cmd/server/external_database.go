package server

import (
	"encoding/json"
	"net/http"
	"path"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

// Read-only access to a workflow's, Relay's or Crew's database over MCP, the
// same query path as the in-product query_workflow_db and the dashboards: the
// workspace service opens db/db.sqlite query-only. Changes stay with the
// Builder, Pulse and steps, under their database write grant (owner,
// 2026-10-09: the app gives people no direct row edits either).

func externalDatabaseDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("query_database", "Read a workflow's, Relay's or Crew's database (db/db.sqlite) read-only: pass sql for one SELECT, WITH or EXPLAIN statement (params bind ? placeholders), action=describe (optional table) for tables and columns, or action=integrity_check. Results are paged: give the query an ORDER BY and pass offset for the next page. Anyone who can open the workflow or Crew may read it. To change data, ask the Builder.", false, false, map[string]any{
		"workflow_id": externalString("Workflow or Relay ID from list_workflows. Pass this or crew_id."),
		"crew_id":     externalString("Crew ID from list_crews. Pass this or workflow_id."),
		"sql":         map[string]any{"type": "string", "minLength": 1, "maxLength": 20000},
		"params":      map[string]any{"type": "array", "maxItems": 100},
		"action":      map[string]any{"type": "string", "enum": []any{"query", "describe", "integrity_check"}},
		"table":       externalString("For describe: one table."),
		"max_rows":    externalInteger(1, 500),
		"offset":      externalInteger(0, 1000000),
	})
}

func (api *StreamingAPI) externalQueryDatabase(w http.ResponseWriter, r *http.Request, args map[string]any, root string) {
	claims := GetUserFromContext(r.Context())
	query := map[string]any{}
	for _, key := range []string{"sql", "params", "action", "table", "max_rows", "offset"} {
		if value, ok := args[key]; ok {
			query[key] = value
		}
	}
	client := api.reportPreviewWorkspaceClient(r.Context(), claims, root)
	out, err := virtualtools.RunWorkflowDBQuery(r.Context(), client, path.Join(root, "db", "db.sqlite"), query)
	if err != nil {
		externalError(w, 400, "query_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if !json.Valid([]byte(out)) {
		externalJSON(w, map[string]any{"result": out})
		return
	}
	_, _ = w.Write([]byte(out))
}
