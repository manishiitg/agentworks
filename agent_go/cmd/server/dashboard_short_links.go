package server

import (
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// Short dashboard links (/d/<code>): the code names one registered dashboard
// resource, nothing more. Opening it needs a signed-in person who can already
// open that dashboard, so a code grants no access and an unknown code and a
// dashboard you cannot see answer the same way.
func (api *StreamingAPI) registerDashboardShortLinkRoutes(router *mux.Router) {
	router.HandleFunc("/dashboards/by-code/{code}", api.handleDashboardByCode).Methods(http.MethodGet)
}

func validDashboardShortCode(code string) bool {
	if len(code) != 10 {
		return false
	}
	for _, c := range code {
		if !strings.ContainsRune(dashboardShortCodeAlphabet, c) {
			return false
		}
	}
	return true
}

func (api *StreamingAPI) handleDashboardByCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		externalError(w, 401, "authentication_required", "sign in to open this dashboard")
		return
	}
	code := strings.TrimSpace(mux.Vars(r)["code"])
	if !validDashboardShortCode(code) {
		externalError(w, 404, "not_found", "dashboard unavailable")
		return
	}
	dashboardQuestionRegistryMu.Lock()
	var registry dashboardQuestionRegistry
	err := readDashboardQuestionJSON(r.Context(), dashboardQuestionRegistryPath, &registry)
	dashboardQuestionRegistryMu.Unlock()
	if err != nil {
		dashboardError(w, err)
		return
	}
	for _, resource := range registry.Resources {
		if resource.Code != code {
			continue
		}
		resolved, err := api.resolveDashboardQuestionResource(r.Context(), claims, resource, "")
		if err != nil || !resolved.Capability.CanView {
			externalError(w, 404, "not_found", "dashboard unavailable")
			return
		}
		externalJSON(w, map[string]any{
			"resource_id": resource.ID, "project_id": resolved.Target.ID, "product": resolved.Target.Kind,
			"root": resource.Root, "document_path": "db/reports/managed/" + resource.Dashboard + "/index.html", "title": resolved.Document.Title,
		})
		return
	}
	externalError(w, 404, "not_found", "dashboard unavailable")
}
