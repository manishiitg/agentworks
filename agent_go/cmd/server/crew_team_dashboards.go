package server

import (
	"encoding/json"
	"log"
	"net/http"
)

// The Crew's published dashboards, artifacts and motion (owner, 6 Nov): everyone in the Team sees all of them, with
// nothing to set. The Dashboards tab (crew_dashboards_tab.go) is the one reader; it is read when the tab opens, not in
// /tab, which every screen re-reads on each panel notice.

type crewTeamDashboard struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Path     string `json:"path"`
	Artifact bool   `json:"artifact,omitempty"`
	Motion   bool   `json:"motion,omitempty"`
	// UpdatedAt is when the page last changed (Unix seconds), from the dashboard list.
	UpdatedAt int64 `json:"updated_at,omitempty"`
	// PublicLink is for the Crew's owner only; teammates see the same rows without it.
	PublicLink *dashboardPublicView `json:"public_link,omitempty"`
}

// crewTeamDashboardRows lists the Crew's published dashboards, artifacts and motion for the
// Dashboards tab: one read, one set of rows. The public link is the owner's, on a Crew that can have one.
func (api *StreamingAPI) crewTeamDashboardRows(w http.ResponseWriter, r *http.Request, t crewTeam, root string, canShare bool) ([]crewTeamDashboard, bool) {
	links := map[string]dashboardPublicRecord{}
	if canShare {
		links = dashboardPublicRecords(root)
	}
	rows := []crewTeamDashboard{}
	for offset := 0; root != ""; {
		var page struct {
			// The list sends discovery rows (dashboard_id, document_path), not the store's Dashboard.
			Dashboards []struct {
				ID           string `json:"dashboard_id"`
				Title        string `json:"title"`
				DocumentPath string `json:"document_path"`
				Artifact     bool   `json:"artifact"`
				Motion       bool   `json:"motion"`
				Managed      bool   `json:"managed"`
				UpdatedAt    int64  `json:"updated_at"`
			} `json:"dashboards"`
			HasMore bool `json:"has_more"`
		}
		status, raw := scheduleHandler(r, api.handleDashboards, http.MethodPost, "/api/dashboards", nil, nil, map[string]any{"workspace": root, "action": "list", "published_only": true, "limit": 100, "offset": offset})
		if status < 200 || status >= 300 {
			var failure struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal(raw, &failure)
			log.Printf("[CREW_DASHBOARDS] project=%s action=list status=%d code=%q reason=%q", t.ProjectID, status, failure.Error.Code, failure.Error.Message)
			externalError(w, http.StatusConflict, "dashboards_unavailable", "This project's dashboards can't be read right now.")
			return nil, false
		}
		if err := r.Context().Err(); err != nil {
			return nil, false
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			log.Printf("[CREW_DASHBOARDS] project=%s action=list status=%d reason=%q", t.ProjectID, status, err)
			externalError(w, http.StatusConflict, "dashboards_unavailable", "This project's dashboards can't be read right now.")
			return nil, false
		}

		for _, d := range page.Dashboards {
			row := crewTeamDashboard{ID: d.ID, Title: d.Title, Path: d.DocumentPath, Artifact: d.Artifact, Motion: d.Motion, UpdatedAt: d.UpdatedAt}
			if canShare && d.Managed {
				var record *dashboardPublicRecord
				if found, ok := links[d.ID]; ok {
					record = &found
				}
				view := dashboardPublicViewOf(d.ID, d.Title, d.Artifact, record)
				if dashboardPublicMacCrewBlocked(t.ProjectID) {
					view.Dialog, view.Blocked = nil, dashboardPublicMacCrew
				}
				row.PublicLink = &view
			}
			rows = append(rows, row)
		}
		if !page.HasMore || len(page.Dashboards) == 0 {
			break
		}
		offset += len(page.Dashboards)
	}
	return rows, true
}

// handleCrewTeamDashboardAction is POST /crew-teams/{project}/dashboards/action: the Artifacts page's buttons for one
// page (public_link with mode, link, delete, undelete), through the Team's own route so the Mac app reaches a server
// Crew's. The dashboard tool decides who may do each, the same as from chat or MCP.
func (api *StreamingAPI) handleCrewTeamDashboardAction(w http.ResponseWriter, r *http.Request) {
	t, claims, ok := crewTeamForTab(w, r, false)
	if !ok || refuseCrewOwnerOnline(w, r, t.ProjectID, claims.UserID) {
		return
	}
	var args struct {
		DashboardID string `json:"dashboard_id"`
		Action      string `json:"action"`
		Mode        string `json:"mode"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&args) != nil {
		externalError(w, 400, "invalid_arguments", "invalid dashboard request")
		return
	}
	body := map[string]any{"workspace": crewProjectRootByID(t.ProjectID), "dashboard_id": args.DashboardID}
	switch args.Action {
	case dashboardPublicAction:
		body["action"], body["mode"] = dashboardPublicAction, args.Mode
	case "link", "delete", "undelete":
		body["action"] = args.Action
	default:
		externalError(w, 400, "invalid_arguments", "unknown dashboard action")
		return
	}
	status, raw := scheduleHandler(r, api.handleDashboards, http.MethodPost, "/api/dashboards", nil, nil, body)
	log.Printf("[CREW_DASHBOARDS] project=%s action=%s dashboard=%s status=%d", t.ProjectID, args.Action, args.DashboardID, status)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}
