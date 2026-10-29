package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Display preferences belong to the caller, never the project or provider account.
type chatPreferences struct {
	ShowContextIndicator bool `json:"show_context_indicator"`
}

func (api *StreamingAPI) chatPreferences(w http.ResponseWriter, r *http.Request, update *bool) {
	w.Header().Set("Cache-Control", "no-store")
	user := GetUserIDFromContext(r.Context())
	if user == "" {
		http.Error(w, "Sign in to change your chat display.", http.StatusUnauthorized)
		return
	}
	path := filepath.ToSlash(filepath.Join(workspaceref.PhysicalPath(user, "preferences"), "chat-display.json"))
	pref := chatPreferences{} // Off until the person turns it on (owner 2026-10-27).
	if update == nil {
		raw, exists, err := readFileFromWorkspace(r.Context(), path)
		if err != nil {
			http.Error(w, "Could not read your chat display. Try again.", http.StatusServiceUnavailable)
			return
		}
		if exists && json.Unmarshal([]byte(raw), &pref) != nil {
			http.Error(w, "Could not read your chat display. Try again.", http.StatusServiceUnavailable)
			return
		}
	} else {
		pref.ShowContextIndicator = *update
		raw, _ := json.Marshal(pref)
		if err := writeRawFileToWorkspace(r.Context(), path, string(raw)); err != nil {
			http.Error(w, "Could not save your chat display. Try again.", http.StatusServiceUnavailable)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pref)
}

func (api *StreamingAPI) handleChatPreferences(w http.ResponseWriter, r *http.Request) {
	var update *bool
	if r.Method == http.MethodPut {
		var body struct {
			Show *bool `json:"show_context_indicator"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || body.Show == nil {
			http.Error(w, "Choose whether to show usage in the chat box.", http.StatusBadRequest)
			return
		}
		update = body.Show
	}
	api.chatPreferences(w, r, update)
}
