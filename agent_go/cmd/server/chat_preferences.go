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

type chatPreferencesUpdate struct {
	Show *bool `json:"show_context_indicator"`
}

func (api *StreamingAPI) chatPreferences(w http.ResponseWriter, r *http.Request, update *chatPreferencesUpdate) {
	w.Header().Set("Cache-Control", "no-store")
	user := GetUserIDFromContext(r.Context())
	if user == "" {
		http.Error(w, "Sign in to change your chat display.", http.StatusUnauthorized)
		return
	}
	path := filepath.ToSlash(filepath.Join(workspaceref.PhysicalPath(user, "preferences"), "chat-display.json"))
	pref := chatPreferences{} // Usage off until the person turns it on (owner 2026-10-27).
	raw, exists, err := readFileFromWorkspace(r.Context(), path)
	if err != nil || (exists && json.Unmarshal([]byte(raw), &pref) != nil) {
		http.Error(w, "Could not read your chat display. Try again.", http.StatusServiceUnavailable)
		return
	}
	if update != nil {
		// A change names only what it changes; the rest stays as saved.
		if update.Show != nil {
			pref.ShowContextIndicator = *update.Show
		}
		saved, _ := json.Marshal(pref)
		if err := writeRawFileToWorkspace(r.Context(), path, string(saved)); err != nil {
			http.Error(w, "Could not save your chat display. Try again.", http.StatusServiceUnavailable)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pref)
}

// chatPreferencesUpdateFromArgs reads a change from a chat or MCP tool call; nil means "read only".
func chatPreferencesUpdateFromArgs(args map[string]any) (*chatPreferencesUpdate, error) {
	var update chatPreferencesUpdate
	if show, ok := args["show_context_indicator"].(bool); ok {
		update.Show = &show
	}
	if update.Show == nil {
		return nil, nil
	}
	return &update, nil
}

func (api *StreamingAPI) handleChatPreferences(w http.ResponseWriter, r *http.Request) {
	var update *chatPreferencesUpdate
	if r.Method == http.MethodPut {
		var body chatPreferencesUpdate
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || body.Show == nil {
			http.Error(w, "Choose a chat display setting to change.", http.StatusBadRequest)
			return
		}
		update = &body
	}
	api.chatPreferences(w, r, update)
}
