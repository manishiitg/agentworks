package server

import (
	"encoding/json"
	"net/http"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
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
			Dashboards []dashboards.Dashboard `json:"dashboards"`
			HasMore    bool                   `json:"has_more"`
		}
		if err := crewPanelRead(r, api.handleDashboards, http.MethodPost, "/api/dashboards", nil, nil, map[string]any{"workspace": root, "action": "list", "published_only": true, "limit": 100, "offset": offset}, &page); err != nil {
			externalError(w, http.StatusConflict, "dashboards_unavailable", "This Crew's dashboards can't be read right now.")
			return nil, false
		}
		for _, d := range page.Dashboards {
			row := crewTeamDashboard{ID: d.ID, Title: d.Title, Path: d.DocumentPath, Artifact: d.Artifact, Motion: d.Motion}
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

// handleCrewTeamDashboardPublicLink is POST /crew-teams/{project}/dashboards/public-link: the card's switch, through the
// Team's own route so the Mac app reaches a server Crew's. The dashboard tool's public_link action decides everything.
func (api *StreamingAPI) handleCrewTeamDashboardPublicLink(w http.ResponseWriter, r *http.Request) {
	t, _, ok := crewTeamForTab(w, r, false)
	if !ok {
		return
	}
	var args struct {
		DashboardID string `json:"dashboard_id"`
		Mode        string `json:"mode"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&args) != nil {
		externalError(w, 400, "invalid_arguments", "invalid public link request")
		return
	}
	status, body := scheduleHandler(r, api.handleDashboards, http.MethodPost, "/api/dashboards", nil, nil, map[string]any{"workspace": crewProjectRootByID(t.ProjectID), "action": dashboardPublicAction, "mode": args.Mode, "dashboard_id": args.DashboardID})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
