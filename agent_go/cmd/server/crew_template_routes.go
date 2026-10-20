package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// Website and MCP template changes share one native installer and current
// Builder checks. The browser never writes project manifests for templates.
func (api *StreamingAPI) handleAgentProfileProjectTemplate(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	vars := mux.Vars(r)
	profileID, id := strings.TrimSpace(vars["id"]), strings.TrimSpace(vars["project_id"])
	if !isProjectProfileID(profileID) || id == "" {
		writeAgentProfileError(w, http.StatusNotFound, "Crew not found")
		return
	}
	if claims := GetUserFromContext(r.Context()); claims == nil || claims.UserID == "" {
		writeAgentProfileError(w, http.StatusUnauthorized, "Sign in to change a Crew template.")
		return
	}
	var request struct {
		Action     string `json:"action"`
		TemplateID string `json:"template_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&request); err != nil || (request.Action != "install_template" && request.Action != "remove_template") {
		writeAgentProfileError(w, http.StatusBadRequest, "Choose a template to add or remove.")
		return
	}
	selector := id

	api.externalCrewProjectCall(w, r, map[string]any{"action": request.Action, "template_id": request.TemplateID}, selector)
}
