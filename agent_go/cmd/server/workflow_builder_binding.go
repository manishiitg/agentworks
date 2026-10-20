package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type workflowSidebarSession struct {
	IsBuilder bool   `json:"is_builder"`
	Kind      string `json:"kind"` // builder, history, run
	updated   time.Time
	live      bool
}

// This is a pointer, not a second conversation store. Old transcripts keep
// their existing paths, permissions and history entries.
type workflowBuilderBinding struct {
	SessionID string `json:"session_id"`
	UserID    string `json:"user_id"`
	UpdatedAt string `json:"updated_at"`
}

func workflowBuilderBindingPath(user, manifestID string) string {
	// Keep the server-owned pointer outside the native Builder's workflow root.
	return chatHistoryRoot(user) + fmt.Sprintf("/workflow-builders/%x.json", sha256.Sum256([]byte(manifestID)))
}

// This pool must be separate from history index locks: discovery reads the
// index while holding the pointer lock, and the two paths can hash identically.
var workflowBuilderPointerLocks [64]sync.Mutex

func workflowBuilderPointerMutex(path string) *sync.Mutex {
	key := sha256.Sum256([]byte(path))
	return &workflowBuilderPointerLocks[int(key[0])%len(workflowBuilderPointerLocks)]
}

func workflowBuilderHistoryKind(id, mode, bot, origin string) string {
	// Interactive and MCP Builder conversations are candidates for the one
	// Builder. Automated provenance, including Pulse without a scheduler ID,
	// stays a run; explicit Run mode and channel identities still win below.
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
	user := GetUserIDFromContext(ctx)
	path := workflowBuilderBindingPath(user, presetID)
	lock := workflowBuilderPointerMutex(path)
	lock.Lock()
	defer lock.Unlock()
	roles, err := api.workflowBuilderSidebarSessions(ctx, workspace)
	if err != nil {
		return nil, err
	}
	content, exists, err := readFileFromWorkspace(ctx, path)
	if err != nil {
		return nil, err
	}
	var binding workflowBuilderBinding
	if exists && (json.Unmarshal([]byte(content), &binding) != nil || binding.UserID != user || binding.SessionID == "") {
		return nil, fmt.Errorf("invalid Builder binding")
	}
	if fresh || !exists {
		binding = workflowBuilderBinding{UserID: user, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		if !fresh {
			var chosen workflowSidebarSession
			for id, role := range roles {
				if role.Kind != "history" {
					continue
				}
				if binding.SessionID == "" || (role.live && !chosen.live) || (role.live == chosen.live && (role.updated.After(chosen.updated) || (role.updated.Equal(chosen.updated) && id > binding.SessionID))) {
					binding.SessionID, chosen = id, role
				}
			}
		}
		if fresh {
			binding.SessionID = uuid.NewString()
		}
		if fresh {
			data, err := json.Marshal(binding)
			if err != nil {
				return nil, err
			}
			if err := writeFileToWorkspace(ctx, path, string(data)); err != nil {
				return nil, err
			}
		}
	}
	response := &workflowBuilderSessionResponse{Success: true, IsBuilder: binding.SessionID != "", Source: "none", SessionID: binding.SessionID,
		PhaseID: "workflow-builder", Status: "completed", PresetQueryID: presetID, WorkspacePath: workspace,
		WorkflowName: workflowNameFromWorkspacePath(workspace), UpdatedAt: binding.UpdatedAt,
		Events: []json.RawMessage{}, LastProcessedIndex: -1, Sessions: roles}
	role, knownConversation := roles[binding.SessionID]
	// Pointer creation/lookup is not conversation activity. Expose the saved
	// transcript or live session timestamp separately for sidebar recency.
	if !role.updated.IsZero() {
		response.ActivityUpdatedAt = role.updated.UTC().Format(time.RFC3339Nano)
	}
	if binding.SessionID != "" {
		roles[binding.SessionID] = workflowSidebarSession{IsBuilder: true, Kind: "builder"}
	}
	api.activeSessionsMux.RLock()
	if current := api.activeSessions[binding.SessionID]; current != nil && current.UserID == user {
		response.Source = "live"
		response.Status = current.Status
	} else if knownConversation {
		response.Source = "workspace"
	}
	api.activeSessionsMux.RUnlock()
	return response, nil
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

// Called only after query ownership, workflow access and the single-Builder
// busy guard have admitted a real interactive send. Notifications/runs never
// reclaim the pointer from a newer New chat.
func recordWorkflowBuilderSend(ctx context.Context, scope workflowBuilderScope, session string) error {
	scope, status := authorizeWorkflowBuilderScope(ctx, scope, true)
	if status != 0 {
		return fmt.Errorf("Builder binding access denied")
	}
	user := GetUserIDFromContext(ctx)
	path := workflowBuilderBindingPath(user, scope.PresetID)
	lock := workflowBuilderPointerMutex(path)
	lock.Lock()
	defer lock.Unlock()
	content, exists, err := readFileFromWorkspace(ctx, path)
	if err != nil {
		return err
	}
	var previous workflowBuilderBinding
	if exists {
		if json.Unmarshal([]byte(content), &previous) != nil || previous.UserID != user {
			return fmt.Errorf("invalid Builder binding")
		}
		if previous.SessionID == session {
			return nil
		}
	}
	data, err := json.Marshal(workflowBuilderBinding{UserID: user, SessionID: session, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return err
	}
	return writeFileToWorkspace(ctx, path, string(data))
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
