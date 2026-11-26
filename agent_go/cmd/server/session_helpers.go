package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// GET /api/sessions/{session_id}/helper-events returns the saved events that
// describe the chat's helper agents (#3245): the chat's agents list builds
// from them, so a long chat keeps helpers the live window has dropped. Same
// read access as the chat's own history.
func (api *StreamingAPI) handleGetSessionHelperEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(mux.Vars(r)["session_id"])
	if sessionID == "" {
		http.Error(w, "session is required", http.StatusBadRequest)
		return
	}
	workspacePath := strings.TrimSpace(r.URL.Query().Get("workspace_path"))
	if status, message := api.authorizeDurableChatRead(r, sessionID, workspacePath); status != 0 {
		http.Error(w, message, status)
		return
	}
	found, err := api.eventStore.ChatHelperEvents(r.Context(), sessionID)
	if err != nil {
		log.Printf("[CHAT_HELPERS] session=%s action=read_failed error=%v", sessionID, err)
		http.Error(w, "failed to read the chat's helpers", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"session_id": sessionID, "events": found})
}
