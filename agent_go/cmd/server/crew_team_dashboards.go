package server

import (
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
}

// handleCrewTeamDashboards is GET /crew-teams/{project}/dashboards. can_share: this person may change who else sees them.
func (api *StreamingAPI) handleCrewTeamDashboards(w http.ResponseWriter, r *http.Request) {
	t, claims, ok := crewTeamForTab(w, r, false)
	if !ok {
		return
	}
	root := crewProjectRootByID(t.ProjectID)
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
			rows = append(rows, crewTeamDashboard{ID: d.ID, Title: d.Title, Path: d.DocumentPath, Artifact: d.Artifact})
		}
		if !page.HasMore || len(page.Dashboards) == 0 {
			break
		}
		offset += len(page.Dashboards)
	}
	w.Header().Set("Cache-Control", "no-store")
	out := map[string]any{"workspace": root, "dashboards": rows, "can_share": t.OwnerID == claims.UserID}
	// A Crew on a Mac: when its dashboards last reached the server (crew_mac_dashboards.go).
	if updated, ok := crewMacDashboardsUpdated(t.ProjectID); ok && crewRelayTwin(t.ProjectID) {
		out["updated"] = "Updated " + roughAgo(time.Since(updated))
		out["note"] = "Buttons on these dashboards need the owner's Mac for now."
	}
	externalJSON(w, out)
}
