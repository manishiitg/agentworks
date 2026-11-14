package server

import (
	"context"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
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
	parentReport, err := crewPublishedDashboardExists(r.Context(), root)
	if err != nil {
		http.Error(w, "Could not load dashboards.", 500)
		return
	}
	for _, binding := range records {
		if binding.CrewRoot != root || binding.Kind != "script" {
			continue
		}
		exists, err := crewPublishedDashboardExists(r.Context(), binding.Root)
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
	defaultDashboard := ""
	if parentReport {
		defaultDashboard = root
	}
	if !parentReport && len(dashboards) > 0 {
		defaultDashboard = dashboards[0].WorkspacePath
	}
	writeAIJSON(w, map[string]any{"dashboards": dashboards, "default_dashboard_workspace": defaultDashboard})
}

// Only current managed publications count as a Crew entry point. Retained HTML
// and revision files after deletion do not keep a removed dashboard alive.
func crewPublishedDashboardExists(ctx context.Context, root string) (bool, error) {
	listed, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: root, Action: "list"})
	if err != nil {
		return false, err
	}
	for _, d := range listed.Dashboards {
		if d.Managed && d.Published != "" {
			return true, nil
		}
	}
	return false, nil
}

func crewHasPublishedDashboardEntry(ctx context.Context, root string) (bool, error) {
	if found, err := crewPublishedDashboardExists(ctx, root); err != nil || found {
		return found, err
	}
	records, err := readCrewExecutables()
	if err != nil {
		return false, err
	}
	for _, binding := range records {
		if binding.CrewRoot == root && binding.Kind == "script" {
			if found, err := crewPublishedDashboardExists(ctx, binding.Root); err != nil || found {
				return found, err
			}
		}
	}
	return false, nil
}
