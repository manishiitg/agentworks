package server

import (
	"encoding/json"
	"fmt"
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
	rawEvents, version, err := api.eventStore.ChatHelperEventsRaw(r.Context(), sessionID)
	if err != nil {
		log.Printf("[CHAT_HELPERS] session=%s action=read_failed error=%v", sessionID, err)
		http.Error(w, "failed to read the chat's helpers", http.StatusInternalServerError)
		return
	}
	etag := fmt.Sprintf(`W/"helpers-%d-%d"`, version, len(rawEvents))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", etag)
	if strings.TrimSpace(r.Header.Get("If-None-Match")) == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	sessionJSON, _ := json.Marshal(sessionID)
	_, _ = w.Write([]byte(`{"session_id":`))
	_, _ = w.Write(sessionJSON)
	_, _ = w.Write([]byte(`,"events":`))
	_, _ = w.Write(rawEvents)
	_, _ = w.Write([]byte("}\n"))
}
