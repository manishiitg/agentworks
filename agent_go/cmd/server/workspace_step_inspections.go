package server

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

const workspaceInspectionPageSize = 20
const workspaceInspectionScanBudget = 200
const workspaceInspectionReadBudget = 24

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
	NextCursor *string                   `json:"next_cursor"`
}

// A cursor contains no foreign parent/session ID. It is only a scoped read
// position; every item is still authorized independently on the next page.
type workspaceInspectionCursor struct {
	Workspace string `json:"workspace"`
	UserID    string `json:"user"`
	UpdatedAt string `json:"updated"`
	RowID     int64  `json:"row"`
}
type workspaceInspectionRecord struct {
	BackgroundAgentLogEntry
	RowID int64
}

// Product navigation returns only explicit step receipts, never the developer
// execution tree, transcript, terminal state, result or tool arguments.
func (api *StreamingAPI) handleWorkspaceStepInspections(w http.ResponseWriter, r *http.Request) {
	workspace, valid := cleanWorkspaceReadPath(r.URL.Query().Get("workspace_path"), true)
	claims := GetUserFromContext(r.Context())
	if IsMultiUserMode() && (claims == nil || strings.TrimSpace(claims.UserID) == "") {
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return
	}
	userID := GetUserIDFromContext(r.Context())
	if userID == "" {
		userID = GetDefaultUserID()
	}
	if !valid || (len(strings.Split(workspace, "/")) != 2 && workflowtypes.RelayReleaseWorkspace(workspace) != workspace) ||
		!workflowInspectionProductAllowed(claims, workspace) ||
		!workspaceReadAllowed(r.Context(), claims, workspace, logicalPathIsDefaultUser, false) || !workflowStepInspectionWorkspace(r.Context(), workspace) {
		http.Error(w, "Workspace not found", http.StatusNotFound)
		return
	}
	var cursor *workspaceInspectionCursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		if len(raw) > 4096 {
			http.Error(w, "Invalid cursor", http.StatusBadRequest)
			return
		}
		cursor = &workspaceInspectionCursor{}
		data, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || json.Unmarshal(data, cursor) != nil || cursor.Workspace != workspace || cursor.UserID != userID || cursor.RowID <= 0 || cursor.UpdatedAt == "" || len(cursor.UpdatedAt) > 128 {
			http.Error(w, "Invalid cursor", http.StatusBadRequest)
			return
		}
	}
	parents := map[string]*ActiveSessionInfo{}
	sessionID := r.URL.Query().Get("session_id")
	if sessionID != "" {
		parent, allowed := api.inspectionParentSessionInAuthorizedWorkspace(r, sessionID, workspace)
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
	if cursor != nil {
		where += " AND (updated_at < ? OR (updated_at = ? AND rowid < ?))"
		args = append(args, cursor.UpdatedAt, cursor.UpdatedAt, cursor.RowID)
	}
	response := workspaceStepInspectionsResponse{Items: []workspaceStepInspection{}}
	_, db, err := openWorkflowStoreDB(r.Context(), workspace, false)
	if err != nil {
		http.Error(w, "Step records unavailable", http.StatusServiceUnavailable)
		return
	}
	if db != nil {
		defer db.Close()
		args = append(args, workspaceInspectionScanBudget+1)
		rows, queryErr := db.QueryContext(r.Context(), `SELECT rowid, session_id, agent_id, kind, parent_execution_id, status, updated_at FROM background_agent_log WHERE `+where+` ORDER BY updated_at DESC, rowid DESC LIMIT ?`, args...)
		if queryErr != nil && !isMissingTableError(queryErr) {
			http.Error(w, "Step records unavailable", http.StatusServiceUnavailable)
			return
		}
		records := []workspaceInspectionRecord{}
		if queryErr == nil {
			for rows.Next() {
				var record workspaceInspectionRecord
				if err = rows.Scan(&record.RowID, &record.SessionID, &record.AgentID, &record.Kind, &record.ParentExecutionID, &record.Status, &record.UpdatedAt); err != nil {
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
		type ancestorKey struct{ session, parent string }
		ancestry := map[ancestorKey]bool{}
		consumed, reads, parentChecks := 0, 0, 0
		for consumed < len(records) && consumed < workspaceInspectionScanBudget && len(response.Items) < workspaceInspectionPageSize {
			record := records[consumed]
			consumed++
			if !inspectionIdentityPart(record.AgentID) {
				continue
			}
			parent, checked := parents[record.SessionID]
			if !checked {
				if parentChecks >= workspaceInspectionReadBudget {
					consumed--
					break
				}
				parentChecks++
				parent, _ = api.inspectionParentSessionInAuthorizedWorkspace(r, record.SessionID, workspace)
				parents[record.SessionID] = parent
			}
			if parent == nil {
				continue
			}
			ancestor := ancestorKey{record.SessionID, record.ParentExecutionID}
			topLevel, checked := ancestry[ancestor]
			if !checked {
				topLevel = workspaceInspectionTopLevel(r, db, workspace, record.BackgroundAgentLogEntry)
				ancestry[ancestor] = topLevel
			}
			if !topLevel {
				continue
			}
			key := inspectionReceiptCacheKey{Store: getWorkspaceAPIURL(), Workspace: workspace, Session: record.SessionID, Execution: record.AgentID, UpdatedAt: record.UpdatedAt, Status: record.Status}
			target, cached := cachedInspectionReceipt(key)
			if !cached {
				if reads >= workspaceInspectionReadBudget {
					consumed--
					break
				}
				reads++
				target = readDurableInspectionReceipt(r.Context(), key)
			}
			if target == nil || target.ExecutionKind != record.Kind {
				continue
			}
			response.Items = append(response.Items, workspaceStepInspection{Inspection: target, Status: record.Status, Parent: workspaceInspectionParent{SessionID: parent.SessionID, WorkspacePath: workspace, Status: parent.Status, TriggeredBy: workspaceInspectionTrigger(parent.TriggeredBy), BotPlatform: workspaceInspectionPlatform(parent.BotPlatform)}})
		}
		if consumed > 0 && consumed < len(records) {
			last := records[consumed-1]
			encoded, _ := json.Marshal(workspaceInspectionCursor{Workspace: workspace, UserID: userID, UpdatedAt: last.UpdatedAt, RowID: last.RowID})
			next := base64.RawURLEncoding.EncodeToString(encoded)
			response.NextCursor = &next
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// Conversation-turn roots are not background log entries. A missing root is
// normal; an existing step ancestor or a cycle makes this a nested candidate.
func workspaceInspectionTopLevel(r *http.Request, db *sql.DB, workspace string, record BackgroundAgentLogEntry) bool {
	if record.ParentExecutionID == "" {
		return true
	}
	var stepAncestor, depth int
	err := db.QueryRowContext(r.Context(), `WITH RECURSIVE ancestors(agent_id,kind,parent_execution_id,depth) AS (
   SELECT agent_id,kind,parent_execution_id,1 FROM background_agent_log
    WHERE workspace_path=? AND session_id=? AND agent_id=?
   UNION ALL
   SELECT entry.agent_id,entry.kind,entry.parent_execution_id,ancestors.depth+1
    FROM background_agent_log entry JOIN ancestors ON entry.agent_id=ancestors.parent_execution_id
    WHERE entry.workspace_path=? AND entry.session_id=? AND ancestors.depth<1000
  ) SELECT COALESCE(MAX(kind IN ('workflow_step','agent','scripted_step','agent_item')),0),COALESCE(MAX(depth),0) FROM ancestors`,
		workspace, record.SessionID, record.ParentExecutionID, workspace, record.SessionID).Scan(&stepAncestor, &depth)
	return err == nil && stepAncestor == 0 && depth < 1000
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

func workflowInspectionProductAllowed(claims *UserClaims, workspace string) bool {
	binding, err := findCrewExecutable(workspace)
	if err != nil {
		return false
	}
	product := "agentworks"
	if binding != nil {
		product = "work"
	}
	return claims == nil || userAllowedProduct(claims, product)
}
