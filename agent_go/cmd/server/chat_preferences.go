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

// chatPreferencesSaved is what is kept: context use shows in the chat box unless the person turned it off (owner,
// 10 Nov, approved mockup "Model Switch Choice"; replaces off-until-turned-on of 27 Oct).
type chatPreferencesSaved struct {
	HideContextIndicator bool `json:"hide_context_indicator,omitempty"`
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
	saved := chatPreferencesSaved{}
	raw, exists, err := readFileFromWorkspace(r.Context(), path)
	if err != nil || (exists && json.Unmarshal([]byte(raw), &saved) != nil) {
		http.Error(w, "Could not read your chat display. Try again.", http.StatusServiceUnavailable)
		return
	}
	if update != nil {
		// A change names only what it changes; the rest stays as saved.
		if update.Show != nil {
			saved.HideContextIndicator = !*update.Show
		}
		data, _ := json.Marshal(saved)
		if err := writeRawFileToWorkspace(r.Context(), path, string(data)); err != nil {
			http.Error(w, "Could not save your chat display. Try again.", http.StatusServiceUnavailable)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(chatPreferences{ShowContextIndicator: !saved.HideContextIndicator})
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
