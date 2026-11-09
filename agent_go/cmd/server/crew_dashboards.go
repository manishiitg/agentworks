package server

import (
	"net/http"
	"sort"
	"strings"
)

// Dashboard discovery reads admitted workflow bindings, never tracking tables.
func (api *StreamingAPI) handleCrewDashboards(w http.ResponseWriter, r *http.Request) {
	if api.projectActions == nil || api.projectActions.registry == nil {
		http.Error(w, "Crew services are unavailable", 503)
		return
	}
	caller := GetUserIDFromContext(r.Context())
	claims := GetUserFromContext(r.Context())
	if caller == "" || claims == nil || userAccessForClaims(claims).Disabled {
		http.Error(w, "Sign in to open this Crew.", 401)
		return
	}
	profile, err := api.projectActions.registry.Resolve("work", 0, caller)
	if err != nil {
		http.Error(w, "Crew is unavailable", 404)
		return
	}
	project, err := resolveCrewProjectBinding(r.Context(), caller, profile, r.URL.Query().Get("crew_id"), "")
	if err != nil {
		http.Error(w, "This Crew is unavailable. Open a Crew shared with you.", 403)
		return
	}
	root := project.Binding.WorkspacePath
	if isExternalCrewDirectCall(r.Context()) && !api.externalCrewReferenceAllowed(r.Context(), claims, root, r.Method != http.MethodGet) {
		http.Error(w, "This Crew is outside the connection's access.", 403)
		return
	}

	records, err := readCrewExecutables()
	if err != nil {
		http.Error(w, "Could not load dashboards.", 500)
		return
	}
	type dashboard struct {
		WorkspacePath string `json:"workspace_path"`
		Name          string `json:"name"`
	}
	dashboards := []dashboard{}
	_, parentReport, err := readFileFromWorkspace(r.Context(), root+"/db/reports/index.html")
	if err != nil {
		http.Error(w, "Could not load dashboards.", 500)
		return
	}
	for _, binding := range records {
		if binding.CrewRoot != root || binding.Kind != "script" {
			continue
		}
		_, exists, err := readFileFromWorkspace(r.Context(), binding.Root+"/db/reports/index.html")
		if err != nil {
			http.Error(w, "Could not load dashboards.", 500)
			return
		}
		if !exists {
			continue
		}
		manifest, found, err := ReadWorkflowManifest(r.Context(), binding.Root)
		if err != nil {
			http.Error(w, "Could not load dashboards.", 500)
			return
		}
		if !found {
			continue
		}
		name := strings.TrimSpace(manifest.Label)
		if name == "" {
			name = "Workflow dashboard"
		}
		dashboards = append(dashboards, dashboard{binding.Root, name})
	}
	sort.Slice(dashboards, func(i, j int) bool {
		if dashboards[i].Name == dashboards[j].Name {
			return dashboards[i].WorkspacePath < dashboards[j].WorkspacePath
		}
		return dashboards[i].Name < dashboards[j].Name
	})
	defaultDashboard := root
	if !parentReport && len(dashboards) > 0 {
		defaultDashboard = dashboards[0].WorkspacePath
	}
	writeAIJSON(w, map[string]any{"dashboards": dashboards, "default_dashboard_workspace": defaultDashboard})
}
