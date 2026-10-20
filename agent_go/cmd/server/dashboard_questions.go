package server

import (
	"github.com/gorilla/mux"
	"net/http"
)

// Dashboard identity and Ask capability are used by the native project chat.
// Questions are submitted through that chat, not a second execution API.
func (api *StreamingAPI) registerDashboardQuestionRoutes(router *mux.Router) {
	router.HandleFunc("/dashboards/{dashboard}", api.handleDashboardQuestionView).Methods(http.MethodGet)
}

func (api *StreamingAPI) dashboardQuestionRequest(w http.ResponseWriter, r *http.Request, revision string, ask bool) (resolvedDashboardQuestion, bool) {
	w.Header().Set("Cache-Control", "no-store")
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		externalError(w, 401, "authentication_required", "sign in to ask about a dashboard")
		return resolvedDashboardQuestion{}, false
	}
	out, err := api.resolveDashboardQuestion(r.Context(), claims, mux.Vars(r)["dashboard"], revision)
	if err != nil {
		dashboardError(w, err)
		return out, false
	}
	if !out.Capability.CanView || ask && !out.Capability.CanAsk {
		externalError(w, 403, out.Capability.Reason, "dashboard questions are unavailable for this viewer")
		return out, false
	}
	return out, true
}

func (api *StreamingAPI) handleDashboardQuestionView(w http.ResponseWriter, r *http.Request) {
	resolved, ok := api.dashboardQuestionRequest(w, r, "", false)
	if !ok {
		return
	}
	externalJSON(w, map[string]any{"resource_id": resolved.Resource.ID, "project_id": resolved.Target.ID, "product": resolved.Target.Kind, "dashboard": resolved.Document, "capability": resolved.Capability})
}
