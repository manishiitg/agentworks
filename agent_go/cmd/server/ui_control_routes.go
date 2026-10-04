package server

import (
	"encoding/json"
	"log"
	"net/http"
	"path"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// AuthMiddleware supplies the user; a binding additionally requires ownership
// of the live chat and current read access to its server-registered workspace.
// Shared read access to somebody else's workflow is NOT chat ownership.
func (api *StreamingAPI) handleUIControl(w http.ResponseWriter, r *http.Request) {
	session := mux.Vars(r)["session_id"]
	fail := func(code string, status int) {
		// Codes only: never log authentication headers, binding tokens or bodies.
		log.Printf("[UI-CONTROL] session=%s http_status=%d code=%s", session, status, code)
		http.Error(w, code, status)
	}
	user := GetUserIDFromContext(r.Context())
	active, exists := api.getActiveSession(session)
	if !exists {
		fail("session_not_active", http.StatusConflict)
		return
	}
	if user == "" || active.UserID != user {
		fail("session_owner_mismatch", http.StatusForbidden)
		return
	}
	b := api.uiBroker()
	workspace := b.scope(session)
	if workspace == "" {
		if restored := restoredWorkUIScope(user, session, active); restored != "" {
			b.setScope(session, restored)
			workspace = restored
		} else if restored := restoredWorkflowUIScopeFor(session, active); restored != "" {
			if access, _ := workflowAccessForWorkspacePath(r.Context(), GetUserFromContext(r.Context()), restored); access != WorkflowAccessNone {
				b.setScope(session, restored)
				workspace = restored
			}
		}
	}
	isWorkflow := strings.HasPrefix(workspace, "Workflow/")
	cleanWorkspace := path.Clean(strings.Trim(strings.TrimSpace(workspace), "/"))
	workspaceRef := workspaceref.MustParse(cleanWorkspace)
	isOwnedWork := workspaceRef.OwnedByOrUnowned(user) && workspaceRef.IsProject()
	if !isWorkflow && !isOwnedWork {
		fail("unsupported_surface", http.StatusConflict)
		return
	}
	if isWorkflow {
		level, manifest := workflowAccessForWorkspacePath(r.Context(), GetUserFromContext(r.Context()), workspace)
		if manifest == nil {
			fail("workspace_unavailable", http.StatusConflict)
			return
		}
		if level == WorkflowAccessNone || !userAllowedWorkflowID(GetUserFromContext(r.Context()), manifest.ID) {
			fail("forbidden", http.StatusForbidden)
			return
		}
	}
	var req struct {
		Operation string     `json:"operation"`
		Version   int        `json:"version"`
		Binding   string     `json:"binding"`
		Token     string     `json:"token"`
		Request   string     `json:"request_id"`
		Status    string     `json:"status"`
		Code      string     `json:"code"`
		State     uiSnapshot `json:"state"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil {
		fail("invalid_request", http.StatusBadRequest)
		return
	}
	if req.Version != uiControlContract.Version {
		fail("version_mismatch", http.StatusConflict)
		return
	}
	var out interface{} = map[string]bool{"ok": true}
	var err error
	switch req.Operation {
	case "bind":
		var client *uiBinding
		client, err = b.bind(session)
		if err == nil {
			out = map[string]interface{}{"binding": client.id, "token": client.token, "workspace": workspace, "version": uiControlContract.Version}
		}
	case "sync":
		out, err = b.syncClient(session, req.Binding, req.Token, req.State)
	case "ack":
		err = b.ack(session, req.Binding, req.Token, req.Request, req.Status, req.Code, req.State)
	case "unbind":
		err = b.unbind(session, req.Binding, req.Token)
	default:
		fail("unsupported_operation", http.StatusBadRequest)
		return
	}
	if err != nil {
		fail(err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(out)
}
