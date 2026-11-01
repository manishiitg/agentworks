package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

// authorizeDurableChatRead applies the durable-chat read rule shared by event
// pages and artifacts. It returns a non-zero HTTP status (and message) when
// the caller may not read this chat.
func (api *StreamingAPI) authorizeDurableChatRead(r *http.Request, sessionID, workspacePath string) (int, string) {
	currentUserID := GetUserIDFromContext(r.Context())
	activeSession, existsInActive := api.getActiveSession(sessionID)
	// A partially rehydrated runtime is not ownership evidence. Use the
	// recorded durable owner until its in-memory identity has been restored.
	if existsInActive && strings.TrimSpace(activeSession.UserID) == "" {
		existsInActive = false
	}
	if existsInActive && activeSession.UserID != "" && activeSession.UserID != currentUserID && !api.crewSessionTabView(r, sessionID) {
		return http.StatusNotFound, "Session not found or access denied"
	}
	if _, _, allowed := chatHistoryWorkspaceAccess(r, workspacePath); !allowed {
		return http.StatusForbidden, "workflow access denied"
	}
	if existsInActive {
		if !api.eventStore.IsDurableChatSession(sessionID) {
			return http.StatusConflict, "Session is not an interactive chat"
		}
		return 0, ""
	}
	ownerID, err := api.eventStore.DurableChatOwner(sessionID)
	if err != nil {
		return http.StatusInternalServerError, "Failed to authorize durable chat"
	}
	if ownerID == "" || !durableChatReadAllowed(r, sessionID, ownerID, workspacePath) && !api.crewSessionTabView(r, sessionID) {
		return http.StatusNotFound, "Session not found or access denied"
	}
	return 0, ""
}

// handleGetChatArtifact returns the complete event behind a row the journal
// summarized because it was too large (or compacted by retention).
func (api *StreamingAPI) handleGetChatArtifact(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := strings.TrimSpace(vars["session_id"])
	artifactID := strings.TrimSpace(vars["artifact_id"])
	if sessionID == "" || artifactID == "" {
		http.Error(w, "session and artifact are required", http.StatusBadRequest)
		return
	}
	workspacePath := strings.TrimSpace(r.URL.Query().Get("workspace_path"))
	if status, message := api.authorizeDurableChatRead(r, sessionID, workspacePath); status != 0 {
		http.Error(w, message, status)
		return
	}
	payload, err := api.eventStore.ReadDurableChatArtifact(sessionID, artifactID)
	if errors.Is(err, events.ErrChatArtifactNotFound) {
		http.Error(w, "artifact not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "failed to read artifact", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(payload)
}
