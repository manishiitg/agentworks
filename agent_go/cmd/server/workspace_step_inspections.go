package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

const workspaceInspectionPageSize = 20

type workspaceStepInspection struct {
	Inspection *orchEvents.ExecutionInspectionTarget `json:"inspection"`
	Status     string                                `json:"status"`
	Parent     workspaceInspectionParent             `json:"parent"`
}
type workspaceInspectionParent struct {
	SessionID     string `json:"session_id"`
	WorkspacePath string `json:"workspace_path"`
	Status        string `json:"status"`
	TriggeredBy   string `json:"triggered_by,omitempty"`
	BotPlatform   string `json:"bot_platform,omitempty"`
}
type workspaceStepInspectionsResponse struct {
	Items      []workspaceStepInspection `json:"items"`
	NextOffset *int                      `json:"next_offset"`
}

// Product navigation returns only explicit step receipts, never the developer
// execution tree, transcript, terminal state, result or tool arguments.
func (api *StreamingAPI) handleWorkspaceStepInspections(w http.ResponseWriter, r *http.Request) {
	workspace, valid := cleanWorkspaceReadPath(r.URL.Query().Get("workspace_path"), true)
	claims := GetUserFromContext(r.Context())
	if !valid || len(strings.Split(workspace, "/")) != 2 ||
		(claims != nil && !userAllowedProduct(claims, "agentworks")) ||
		!workspaceReadAllowed(r.Context(), claims, workspace, logicalPathIsDefaultUser, false) || !workflowStepInspectionWorkspace(r.Context(), workspace) {
		http.Error(w, "Workspace not found", http.StatusNotFound)
		return
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		var err error
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > 10000 {
			http.Error(w, "Invalid offset", http.StatusBadRequest)
			return
		}
	}
	parents := map[string]*ActiveSessionInfo{}
	sessionID := r.URL.Query().Get("session_id")
	if sessionID != "" {
		parent, allowed := api.inspectionParentSession(r, sessionID, workspace)
		if !allowed {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		parents[sessionID] = parent
	}
	excluded := r.URL.Query()["exclude_session_id"]
	if len(excluded) > 50 {
		http.Error(w, "Too many exclusions", http.StatusBadRequest)
		return
	}
	where := "workspace_path = ? AND kind IN ('workflow_step','agent','scripted_step')"
	args := []any{workspace}
	if sessionID != "" {
		where += " AND session_id = ?"
		args = append(args, sessionID)
	}
	for _, id := range excluded {
		if !inspectionIdentityPart(id) {
			http.Error(w, "Invalid session", http.StatusBadRequest)
			return
		}
		where += " AND session_id <> ?"
		args = append(args, id)
	}
	response := workspaceStepInspectionsResponse{Items: []workspaceStepInspection{}}
	_, db, err := openPulseStoreDB(r.Context(), workspace, false)
	if err != nil {
		http.Error(w, "Step records unavailable", http.StatusServiceUnavailable)
		return
	}
	if db != nil {
		defer db.Close()
		args = append(args, workspaceInspectionPageSize+1, offset)
		rows, queryErr := db.QueryContext(r.Context(), `SELECT session_id, agent_id, kind, parent_execution_id, status, updated_at FROM background_agent_log WHERE `+where+` ORDER BY updated_at DESC, session_id, agent_id LIMIT ? OFFSET ?`, args...)
		if queryErr != nil && !isMissingTableError(queryErr) {
			http.Error(w, "Step records unavailable", http.StatusServiceUnavailable)
			return
		}
		records := []BackgroundAgentLogEntry{}
		if queryErr == nil {
			for rows.Next() {
				var record BackgroundAgentLogEntry
				if err = rows.Scan(&record.SessionID, &record.AgentID, &record.Kind, &record.ParentExecutionID, &record.Status, &record.UpdatedAt); err != nil {
					break
				}
				records = append(records, record)
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err != nil {
				http.Error(w, "Step records unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		if len(records) > workspaceInspectionPageSize {
			next := offset + workspaceInspectionPageSize
			response.NextOffset = &next
			records = records[:workspaceInspectionPageSize]
		}
		ancestry := map[string]map[string]BackgroundAgentLogEntry{}
		for _, record := range records {
			if !inspectionIdentityPart(record.AgentID) {
				continue
			}
			parent, checked := parents[record.SessionID]
			if !checked {
				parent, _ = api.inspectionParentSession(r, record.SessionID, workspace)
				parents[record.SessionID] = parent
			}
			if parent == nil {
				continue
			}
			if _, loaded := ancestry[record.SessionID]; !loaded {
				ancestry[record.SessionID] = workspaceInspectionAncestry(r, db, workspace, record.SessionID)
			}
			if !workspaceInspectionTopLevel(record, ancestry[record.SessionID]) {
				continue
			}
			key := inspectionReceiptCacheKey{Store: getWorkspaceAPIURL(), Workspace: workspace, Session: record.SessionID, Execution: record.AgentID, UpdatedAt: record.UpdatedAt, Status: record.Status}
			target, cached := cachedInspectionReceipt(key)
			if !cached {
				target = readDurableInspectionReceipt(r.Context(), key)
			}
			if target == nil || target.ExecutionKind != record.Kind {
				continue
			}
			response.Items = append(response.Items, workspaceStepInspection{Inspection: target, Status: record.Status, Parent: workspaceInspectionParent{SessionID: parent.SessionID, WorkspacePath: workspace, Status: parent.Status, TriggeredBy: workspaceInspectionTrigger(parent.TriggeredBy), BotPlatform: workspaceInspectionPlatform(parent.BotPlatform)}})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func workspaceInspectionAncestry(r *http.Request, db *sql.DB, workspace, session string) map[string]BackgroundAgentLogEntry {
	// A missing or truncated ancestry fails closed for nested candidates.
	records := map[string]BackgroundAgentLogEntry{}
	rows, err := db.QueryContext(r.Context(), `SELECT agent_id, kind, parent_execution_id FROM background_agent_log WHERE workspace_path=? AND session_id=? ORDER BY updated_at DESC LIMIT 1000`, workspace, session)
	if err != nil {
		return records
	}
	defer rows.Close()
	for rows.Next() {
		var item BackgroundAgentLogEntry
		if rows.Scan(&item.AgentID, &item.Kind, &item.ParentExecutionID) != nil {
			return nil
		}
		records[item.AgentID] = item
	}
	if rows.Err() != nil {
		return nil
	}
	return records
}
func workspaceInspectionTopLevel(record BackgroundAgentLogEntry, records map[string]BackgroundAgentLogEntry) bool {
	seen := map[string]bool{record.AgentID: true}
	for parent := record.ParentExecutionID; parent != ""; {
		if seen[parent] || len(seen) >= 1000 {
			return false
		}
		seen[parent] = true
		item, found := records[parent]
		if !found || workflowStepInspectionKind(item.Kind) {
			return false
		}
		parent = item.ParentExecutionID
	}
	return true
}

func workspaceInspectionTrigger(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "mcp", "schedule", "scheduled", "cron", "webhook", "manual":
		return strings.ToLower(strings.TrimSpace(value))
	}
	return ""
}
func workspaceInspectionPlatform(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "mcp", "slack", "whatsapp":
		return strings.ToLower(strings.TrimSpace(value))
	}
	return ""
}
