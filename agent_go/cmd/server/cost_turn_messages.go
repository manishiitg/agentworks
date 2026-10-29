package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// Cost visibility does not grant access to private chat content. This separate
// read uses the same chat authorization as its events and saved artifacts.
func (api *StreamingAPI) handleUsageTurnMessages(w http.ResponseWriter, r *http.Request) {
	session := strings.TrimSpace(mux.Vars(r)["session_id"])
	workspace := strings.TrimSpace(r.URL.Query().Get("workspace_path"))
	ids := r.URL.Query()["execution_id"]
	if session == "" || len(ids) == 0 || len(ids) > 100 {
		http.Error(w, "session and up to 100 executions are required", http.StatusBadRequest)
		return
	}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" || len(id) > 256 {
			http.Error(w, "invalid execution", http.StatusBadRequest)
			return
		}
	}
	if api.eventStore == nil {
		http.Error(w, "chat history unavailable", http.StatusServiceUnavailable)
		return
	}
	if status, message := api.authorizeDurableChatRead(r, session, workspace); status != 0 {
		http.Error(w, message, status)
		return
	}
	messages, err := api.eventStore.UsageTurnMessages(r.Context(), session, ids)
	if err != nil {
		http.Error(w, "could not load turn messages", http.StatusInternalServerError)
		return
	}
	log.Printf("[USAGE_MESSAGES] session=%s requested=%d found=%d", session, len(ids), len(messages))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"messages": messages})
}
