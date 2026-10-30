package server

import (
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"net/http"
)

func (api *StreamingAPI) handleCrewResults(w http.ResponseWriter, r *http.Request) {
	binding, _, ok := api.authorizeCrewProjectRead(r, mux.Vars(r)["project_id"])
	claims := GetUserFromContext(r.Context())
	if !ok || claims == nil || !crewProjectOwnedByCaller(claims.UserID, binding.WorkspacePath) {
		writeAgentProfileError(w, 404, "project not found")
		return
	}
	// This owner workspace panel is not a second file-read surface for narrowed tokens.
	if token := claims.AccessToken; token != nil && (token.FileGuard != nil || !token.Allows("crews:read") || !token.AllowsCrew(mux.Vars(r)["project_id"])) {
		writeAgentProfileError(w, 403, "Use a full project connection to see Results.")
		return
	}
	results, err := workproduct.ReadResults(binding.WorkspacePath)
	if err != nil {
		writeAgentProfileError(w, 503, "Results could not be loaded. Try again.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeAgentProfileJSON(w, 200, results)
}
