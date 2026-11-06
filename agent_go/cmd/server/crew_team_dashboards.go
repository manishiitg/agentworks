package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

// The Team tab's Dashboards card (owner, 6 Nov, approved mockup): everyone in the Team sees all the Crew's dashboards and
// artifacts, with nothing to set; Share stays for people outside the Team. Read when the card opens, not in /tab, which
// every screen re-reads on each panel notice.

type crewTeamDashboard struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Path     string `json:"path"`
	Artifact bool   `json:"artifact,omitempty"`
	// PublicLink is for the Crew's owner only; teammates see the same rows without it.
	PublicLink *dashboardPublicView `json:"public_link,omitempty"`
}

// handleCrewTeamDashboards is GET /crew-teams/{project}/dashboards. can_share: this person may change who else sees them.
func (api *StreamingAPI) handleCrewTeamDashboards(w http.ResponseWriter, r *http.Request) {
	t, claims, ok := crewTeamForTab(w, r, false)
	if !ok {
		return
	}
	root := crewProjectRootByID(t.ProjectID)
	canShare := t.OwnerID == claims.UserID
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
			return
		}
		for _, d := range page.Dashboards {
			row := crewTeamDashboard{ID: d.ID, Title: d.Title, Path: d.DocumentPath, Artifact: d.Artifact}
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
	w.Header().Set("Cache-Control", "no-store")
	out := map[string]any{"workspace": root, "dashboards": rows, "can_share": canShare}
	// A Crew on a Mac: when its dashboards last reached the server (crew_mac_dashboards.go).
	if updated, ok := crewMacDashboardsUpdated(t.ProjectID); ok && crewRelayTwin(t.ProjectID) {
		out["updated"] = "Updated " + roughAgo(time.Since(updated))
		out["note"] = "Buttons on these dashboards work on the owner's Mac for now."
	}
	externalJSON(w, out)
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
