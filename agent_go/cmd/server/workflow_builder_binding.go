package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type workflowSidebarSession struct {
	IsBuilder bool   `json:"is_builder"`
	Kind      string `json:"kind"` // builder, history, run
	updated   time.Time
	live      bool
}

func workflowBuilderHistoryKind(id, mode, bot, origin string) string {
	if origin != "" && origin != "interactive" && origin != "notification" && origin != "external_builder" {
		return "run"
	}
	if normalizeChatHistoryWorkshopMode(mode) == "run" || bot != "" || separateWorkflowConversation(id) ||
		isScheduledSessionIdentity(id, "") || strings.HasPrefix(id, "bot-") {
		return "run"
	}
	return "history"
}

func (api *StreamingAPI) workflowBuilderSidebarSessions(ctx context.Context, workspace string) (map[string]workflowSidebarSession, error) {
	viewer := GetUserIDFromContext(ctx)
	access, _ := workflowAccessForWorkspacePath(ctx, GetUserFromContext(ctx), workspace)
	sessions, handled := listWorkflowBuilderHistoryFromDisk(viewer, workspace, 0, "")
	if !handled {
		var err error
		sessions, _, err = listChatHistorySessionsFromWorkspaceIndex(viewer, workspace, 0, 0)
		if err != nil {
			return nil, err
		}
	}
	roles := map[string]workflowSidebarSession{}
	for _, session := range sessions {
		if !builderConversationVisibleTo(session.UserID, viewer, access) && !isWorkflowBotHistory(session) {
			continue
		}
		origin := ""
		if session.Runtime != nil {
			origin = session.Runtime.ChatPolicyRoleOrigin
		}
		updated, _ := time.Parse(time.RFC3339Nano, session.UpdatedAt)
		roles[session.SessionID] = workflowSidebarSession{Kind: workflowBuilderHistoryKind(session.SessionID, session.WorkshopMode, session.BotPlatform, origin), updated: updated}
	}
	api.activeSessionsMux.RLock()
	defer api.activeSessionsMux.RUnlock()
	for id, session := range api.activeSessions {
		if session == nil || session.UserID != viewer || !workflowBuilderSessionMatches(session, "", workspace) {
			continue
		}
		origin := ""
		if role, ok := chatPolicyRoleSnapshotFor(id); ok {
			origin = role.Origin
		}
		kind := workflowBuilderHistoryKind(id, session.WorkshopMode, session.BotPlatform, origin)
		if session.AgentMode != "workflow_phase" || session.ParentSessionID != "" || session.SessionKind != "" || isScheduledSessionIdentity(id, session.TriggeredBy) ||
			(session.PhaseID != "" && session.PhaseID != "workflow-builder") {
			kind = "run"
		}
		if previous, ok := roles[id]; ok && previous.Kind == "run" {
			kind = "run"
		}
		roles[id] = workflowSidebarSession{Kind: kind, updated: session.LastActivity, live: true}
	}
	return roles, nil
}

func (api *StreamingAPI) resolveWorkflowBuilderBinding(ctx context.Context, presetID, workspace string, fresh bool) (*workflowBuilderSessionResponse, error) {
	return api.goalOwnerTabSummary(ctx, workflowBuilderScope{PresetID: presetID, Workspace: workspace}, fresh)
}

func (api *StreamingAPI) currentWorkflowBuilderSummary(ctx context.Context, presetID, workspace string) (*workflowBuilderSessionResponse, error) {
	return api.resolveWorkflowBuilderBinding(ctx, presetID, workspace, false)
}

type workflowBuilderScope struct {
	PresetID   string   `json:"preset_query_id"`
	Workspace  string   `json:"workspace_path"`
	SessionIDs []string `json:"session_ids,omitempty"`
}

func authorizeWorkflowBuilderScope(ctx context.Context, scope workflowBuilderScope, write bool) (workflowBuilderScope, int) {
	claims := GetUserFromContext(ctx)
	if IsMultiUserMode() && claims == nil {
		return scope, http.StatusUnauthorized
	}
	scope.Workspace = strings.Trim(strings.TrimSpace(scope.Workspace), "/")
	if !strings.HasPrefix(scope.Workspace, "Workflow/") {
		return scope, http.StatusBadRequest
	}
	access, manifest := workflowAccessForWorkspacePath(ctx, claims, scope.Workspace)
	if manifest == nil {
		return scope, http.StatusNotFound
	}
	if access == WorkflowAccessNone || !userAllowedWorkflowID(claims, manifest.ID) || (write && access == WorkflowAccessRead) {
		return scope, http.StatusForbidden
	}
	if scope.PresetID != "" && scope.PresetID != manifest.ID {
		return scope, http.StatusBadRequest
	}
	scope.PresetID = manifest.ID
	if scope.PresetID == "" {
		return scope, http.StatusBadRequest
	}
	return scope, 0
}

// One batch for all visible workflows, without transcripts or runtime actions.
func (api *StreamingAPI) handleWorkflowBuilderBindings(w http.ResponseWriter, r *http.Request) {
	if IsMultiUserMode() && GetUserFromContext(r.Context()) == nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	var request struct {
		Workflows []workflowBuilderScope `json:"workflows"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request); err != nil || len(request.Workflows) > 100 {
		http.Error(w, "invalid workflow list", http.StatusBadRequest)
		return
	}
	bindings := map[string]*workflowBuilderSessionResponse{}
	for _, requested := range request.Workflows {
		if len(requested.SessionIDs) > 1000 {
			continue
		}
		scope, status := authorizeWorkflowBuilderScope(r.Context(), requested, false)
		if status != 0 {
			continue
		} // No metadata for a denied/invalid workflow.
		if _, duplicate := bindings[scope.PresetID]; duplicate {
			continue
		}
		binding, err := api.currentWorkflowBuilderSummary(r.Context(), scope.PresetID, scope.Workspace)
		if err != nil {
			continue
		}
		for _, id := range scope.SessionIDs {
			if len(id) > 256 {
				continue
			}
			if _, known := binding.Sessions[id]; !known {
				binding.Sessions[id] = workflowSidebarSession{Kind: "history"}
			}
		}
		bindings[scope.PresetID] = binding
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "bindings": bindings})
}

// Register only an admitted interactive Builder session. Automated runs and
// notifications never become owner-controlled tabs.
func recordWorkflowBuilderSend(ctx context.Context, scope workflowBuilderScope, session string) error {
	scope, status := authorizeWorkflowBuilderScope(ctx, scope, true)
	if status != 0 {
		return fmt.Errorf("Builder binding access denied")
	}
	user := GetUserIDFromContext(ctx)
	store := defaultProductConversationRegistryStore()
	tabs, err := store.projectChats(ctx, user, goalChatProfile, scope.PresetID)
	if err != nil {
		return err
	}
	main := true
	for _, tab := range tabs {
		if tab.WorkspacePath == scope.Workspace && tab.SessionID == session {
			return nil
		}
		if tab.ConversationKey == crewOwnerDefaultChatKey(scope.PresetID, user) {
			main = false
		}
	}
	_, err = storeGoalOwnerTab(ctx, scope, "Builder", main, session)
	return err
}

func (api *StreamingAPI) handleNewWorkflowBuilderSession(w http.ResponseWriter, r *http.Request) {
	var requested workflowBuilderScope
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&requested); err != nil {
		http.Error(w, "invalid workflow", http.StatusBadRequest)
		return
	}
	scope, status := authorizeWorkflowBuilderScope(r.Context(), requested, true)
	if status != 0 {
		http.Error(w, "Builder access denied", status)
		return
	}
	binding, err := api.resolveWorkflowBuilderBinding(r.Context(), scope.PresetID, scope.Workspace, true)
	if err != nil {
		http.Error(w, "could not start Builder chat", http.StatusInternalServerError)
		return
	}
	publishSessionsChanged()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(binding)
}
