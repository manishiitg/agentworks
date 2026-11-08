package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const externalRunSessionMarker = "run-"

// External run conversations keep their own identity, independently of any
// Builder chat routes. A token still controls only its own issued prefix.
func externalRunSessionID(sessionID string) bool {
	return strings.HasPrefix(sessionID, externalRunSessionMarker) ||
		(strings.HasPrefix(sessionID, "pat-") && strings.Contains(sessionID, "-"+externalRunSessionMarker))
}

func externalRunSessionMode(mode, phase string) bool {
	return mode == "workflow" || mode == "workflow_phase" && phase == "workflow-builder"
}

// The dispatcher already selected a currently accessible workflow. Resolve
// only this caller's external run inside that exact workspace. Entry points
// retain their existing token-prefix/started-by checks; never discover
// or inherit its Builder tabs or another person's conversation.
func (api *StreamingAPI) externalRunSession(r *http.Request, workspace, sessionID string) (*ActiveSessionInfo, bool, error) {
	user := strings.TrimSpace(GetUserIDFromContext(r.Context()))
	missing := func() (*ActiveSessionInfo, bool, error) { return nil, false, fmt.Errorf("run session not found") }
	if user == "" || !externalRunSessionID(sessionID) {
		return missing()
	}
	if active, ok := api.getActiveSession(sessionID); ok {
		if active.UserID != user || normalizeChatHistoryWorkspacePath(active.WorkspacePath) != normalizeChatHistoryWorkspacePath(workspace) ||
			!externalRunSessionMode(active.AgentMode, active.PhaseID) || isScheduledSessionIdentity(sessionID, active.TriggeredBy) {
			return missing()
		}
		return active, false, nil
	}
	if api.eventStore != nil {
		if owner := api.eventStore.GetSessionOwner(sessionID); owner != "" && owner != user {
			return missing()
		}
	}
	// These existing readers are scoped to this workflow on both local and
	// hosted storage. The general chat-history reader can fall back elsewhere.
	raw, exists, err := readWorkflowScopedChatHistoryConversationDirect(sessionID, workspace)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		raw, exists, err = readWorkflowScopedChatHistoryConversationFromWorkspace(sessionID, workspace)
	}
	if err != nil {
		return nil, false, err
	}
	var saved struct {
		SessionID string                   `json:"session_id"`
		UserID    string                   `json:"user_id"`
		AgentMode string                   `json:"agent_mode"`
		PhaseID   string                   `json:"phase_id"`
		Runtime   *ChatHistoryAgentRuntime `json:"runtime"`
	}
	if !exists || json.Unmarshal(raw, &saved) != nil || saved.SessionID != sessionID || saved.UserID != user ||
		!externalRunSessionMode(saved.AgentMode, saved.PhaseID) || isScheduledSessionIdentity(sessionID, "") {
		return missing()
	}
	if saved.Runtime != nil && strings.TrimSpace(saved.Runtime.WorkspacePath) != "" &&
		normalizeChatHistoryWorkspacePath(saved.Runtime.WorkspacePath) != normalizeChatHistoryWorkspacePath(workspace) {
		return missing()
	}
	return nil, true, nil
}
