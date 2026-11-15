package server

import (
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	"log"
	"net/http"
)

// Only source selectors cross the UI boundary; destination paths are resolved
// from the protected accepted mapping again when Open is pressed.
type teamOnlineItemSelector struct {
	TeamProjectID   string `json:"team_project_id"`
	SourceProjectID string `json:"source_project_id"`
	Kind            string `json:"item_kind"`
	ItemID          string `json:"dashboard_id"`
}

// Include accepted items in the ordinary Team list, never every page in the
// virtual user's private home. Both screen and MCP readers call this reader.
func (api *StreamingAPI) crewTeamDashboardOnlineRows(w http.ResponseWriter, r *http.Request, team crewTeam, root string, rows []crewTeamDashboard) ([]crewTeamDashboard, bool) {
	if !team.On || team.OnlineHome == "" {
		return rows, true
	}
	fail := func(err error) ([]crewTeamDashboard, bool) {
		log.Printf("[CREW_DASHBOARDS] project=%s action=online_list reason=%q", team.ProjectID, err)
		externalError(w, 409, "dashboards_unavailable", "The Team's online items can't be read right now.")
		return nil, false
	}
	if _, err := teamOnlineHome(r.Context(), team.ProjectID); err != nil {
		return fail(err)
	}
	mappings, err := readTeamOnlineMappings(r.Context(), triggerTarget{CrewID: team.OnlineHome})
	if err != nil {
		return fail(err)
	}
	claims := GetUserFromContext(r.Context())
	for _, m := range mappings {
		if m.Kind != "dashboard" && m.Kind != "artifact" {
			continue
		}
		location, found, err := api.teamOnlineAcceptedLocation(r.Context(), team.ProjectID, m.SourceProjectID, m.Kind, m.ItemID)
		if err != nil {
			return fail(err)
		}
		if !found {
			continue
		}
		published, err := dashboardWorkspaceRequest(r.Context(), dashboards.Request{Root: location.Workspace, ID: location.ItemID, Action: "get", Guard: dashboardFileGuard(claims)})
		if err != nil {
			return fail(err)
		}
		if published.Dashboard == nil {
			continue
		}
		d := published.Dashboard
		// Prefer the accepted Team item over its editable source in the same list,
		// and do not repeat a destination already listed as a direct Team page.
		kept := rows[:0]
		for _, row := range rows {
			if (m.SourceProjectID == team.ProjectID && row.ID == m.ItemID && row.Online == nil) || (firstNonEmptyTrimmed(row.Workspace, root) == location.Workspace && row.ID == location.ItemID) {
				continue
			}
			kept = append(kept, row)
		}
		rows = append(kept, crewTeamDashboard{ID: d.ID, Title: d.Title, Path: d.DocumentPath, Workspace: location.Workspace, Artifact: d.Artifact, Motion: d.Motion, Online: &teamOnlineItemSelector{TeamProjectID: team.ProjectID, SourceProjectID: m.SourceProjectID, Kind: m.Kind, ItemID: m.ItemID}})
	}
	log.Printf("[CREW_DASHBOARDS] project=%s action=online_list items=%d", team.ProjectID, len(rows))
	return rows, true
}
