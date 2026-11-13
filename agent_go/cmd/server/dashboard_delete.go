package server

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

const dashboardRemovedMessage = "This dashboard was removed. Ask the person who shared it."

// Admission is the ordinary publish admission in handleDashboards. Files and
// runs are not deleted. Removed links retain only an opaque code receipt so an
// old URL can explain what happened; it cannot resolve to a project or page.
func (api *StreamingAPI) deleteDashboard(w http.ResponseWriter, r *http.Request, target dashboardTarget, req dashboards.Request) {
	if req.Guard != nil && !req.Guard.Allows(dashboards.StatePath(req.ID), true) {
		externalError(w, 403, "protected_path", "outside connection write grants")
		return
	}
	check := req
	check.Action = "get"
	if _, err := dashboardWorkspaceRequest(r.Context(), check); err != nil {
		dashboardError(w, err)
		return
	}
	dashboardPublicMu.Lock()
	defer dashboardPublicMu.Unlock()
	if err := removeDashboardLinks(r.Context(), target.Root, req.ID); err != nil {
		dashboardError(w, err)
		return
	}
	result, err := dashboardWorkspaceRequest(r.Context(), req)
	if err != nil {
		dashboardError(w, err)
		return
	}
	log.Printf("[DASHBOARD_DELETE] project=%s dashboard=%s deleted=%t files_kept=true", target.ID, req.ID, result.Deleted)
	externalJSON(w, result)
}

// Called with dashboardPublicMu held, the same lock public-link saves use.
func removeDashboardLinks(ctx context.Context, root, id string) error {
	// Replace public snapshots with a receipt, removing the frozen page/data.
	dir, err := dashboardPublicDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if !validDashboardPublicCode(entry.Name()) {
			continue
		}
		record, err := readDashboardPublicRecord(dir, entry.Name())
		if err != nil {
			return err
		}
		if record.Root != root || record.Dashboard != id {
			continue
		}
		if err := saveDashboardPublicPage(dashboardPublicRecord{Code: record.Code, Removed: true}, nil); err != nil {
			return err
		}
	}
	dashboardQuestionRegistryMu.Lock()
	defer dashboardQuestionRegistryMu.Unlock()
	var registry dashboardQuestionRegistry
	if err := readDashboardQuestionJSON(ctx, dashboardQuestionRegistryPath, &registry); err != nil {
		return err
	}
	changed := false
	for i, record := range registry.Resources {
		if record.Root == root && record.Dashboard == id {
			registry.Resources[i] = dashboardQuestionResource{ID: record.ID, Code: record.Code, Removed: true}
			changed = true
		}
	}
	if changed {
		return saveDashboardQuestionJSON(ctx, dashboardQuestionRegistryPath, registry)
	}
	return nil
}
