package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	"github.com/manishiitg/coding-agent-loop/workspace/teamonline"
)

type teamDashboardBegin struct {
	TeamProjectID string `json:"team_project_id"`
	HomeProjectID string `json:"home_project_id"`
	Address       string `json:"address"`
	SubmissionID  string `json:"submission_id"`
}

// Explicit begin is separate from ordinary asks and non-mutating previews.
// It validates the actual owned publication before creating the normal home;
// it never applies data, moves routines, or launches an AI turn.
func (api *StreamingAPI) beginTeamDashboardHandoff(ctx context.Context, in teamOnlineRequest) (teamDashboardBegin, error) {
	out := teamDashboardBegin{}
	claims := GetUserFromContext(ctx)
	if IsLocalMode() || claims == nil || claims.Scope != "" && (claims.AccessToken == nil || !claims.AccessToken.Allows("crews:write")) || !crewTeamClaims(claims, in.TeamProjectID, true) || !userAccessForClaims(claims).CanEdit || api.agentProfiles == nil {
		return out, fmt.Errorf("use your own Builder connection to begin this Team handoff")
	}
	if in.Kind != "dashboard" && in.Kind != "artifact" {
		return out, fmt.Errorf("choose a published dashboard or Artifact")
	}
	if err := in.validate(); err != nil {
		return out, err
	}
	// The exact-pair Mac export has its own local identity. Keep the original
	// connection's source-Crew consent before crossing that boundary.
	if !crewTeamClaims(claims, in.SourceProjectID, true) {
		return out, fmt.Errorf("allow the source Crew in your Builder connection")
	}
	team, err := teamOnlineHome(ctx, in.TeamProjectID)
	if err != nil {
		return out, err
	}
	if token := claims.AccessToken; token != nil {
		if team.OnlineHome == "" && !token.AllCrews || team.OnlineHome != "" && !token.AllowsCrew(team.OnlineHome) {
			return out, fmt.Errorf("allow the Team Crew in your Builder connection before beginning")
		}
		if guard := token.FileGuard; guard != nil {
			for _, p := range []string{"schedules.json", "functions.json", "code/triggers", "db"} {
				if !guard.Allows(p, true) {
					return out, fmt.Errorf("this connection does not allow the Team item files")
				}
			}
		}
	}
	source, p, _, closePackage, err := api.fetchTeamOnlineSource(ctx, in, false)
	if err != nil {
		return out, err
	}
	defer closePackage()
	if source.CrewOwner != claims.UserID || source.CrewID != in.SourceProjectID || time.Now().After(p.expires) {
		return out, fmt.Errorf("choose a current item from your own Crew")
	}
	file, err := p.root.Open(teamonline.Descriptor)
	if err != nil {
		return out, err
	}
	item, err := teamonline.DecodeItem(file)
	file.Close()
	if err != nil {
		return out, err
	}
	if item.ID != in.ItemID || item.Revision != in.Revision || item.Snapshot.Artifact != (in.Kind == "artifact") {
		return out, fmt.Errorf("the published item changed; begin again with its current revision")
	}
	// This member is rechecked after the exact-pair round trip by the ordinary
	// home factory. A request cannot nominate a destination or a Main identity.
	created := team.OnlineHome == ""
	home, err := api.ensureCrewTeamOnlineHome(ctx, in.TeamProjectID)
	if err != nil {
		return out, err
	}
	target, err := api.dashboardTarget(ctx, claims, crewProjectRootByID(home))
	if err != nil || !dashboardAuthoringAllowed(claims, &target) {
		return out, fmt.Errorf("the Team Crew is unavailable to this Builder connection")
	}
	team, err = teamOnlineHome(ctx, in.TeamProjectID)
	if err != nil {
		return out, err
	}
	for _, row := range crewTeamReachableIn(ctx, team, claims.UserID) {
		if row.Kind == "team_agent" && row.Team == in.TeamProjectID && strings.HasPrefix(row.Address, "agent:") {
			state := "existing"
			if created {
				state = "created"
			}
			log.Printf("[CREW_TEAM] event=handoff_begin home=%s person=%s team=%s source=%s item=%s revision=%s submission=%s", state, claims.UserID, in.TeamProjectID, source.CrewID, in.ItemID, in.Revision, in.SubmissionID)
			return teamDashboardBegin{TeamProjectID: in.TeamProjectID, HomeProjectID: home, Address: row.Address, SubmissionID: in.SubmissionID}, nil
		}
	}
	return out, fmt.Errorf("the Team agent is not ready; try beginning again in a moment")
}

// The source-side tool derives the Team and saved connection, never accepting a
// caller's server URL or a model-supplied filesystem destination.
func (api *StreamingAPI) beginDashboardFromSource(ctx context.Context, root, id, revision, submission string) (teamDashboardBegin, error) {
	out := teamDashboardBegin{}
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.Scope != "" && (claims.AccessToken == nil || !claims.AccessToken.Allows("crews:write")) {
		return out, fmt.Errorf("use your own Builder connection")
	}
	target, err := api.dashboardTarget(ctx, claims, root)
	if err != nil || target.Kind != "crew" || !dashboardAuthoringAllowed(claims, &target) {
		return out, fmt.Errorf("begin from your own published item")
	}
	source, err := resolveTriggerTarget(ctx, claims, "workspace:work:"+target.ID)
	if err != nil || source.CrewOwner != claims.UserID || !crewTeamClaims(claims, source.CrewID, true) {
		return out, fmt.Errorf("begin from your own published item")
	}
	pub, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: root, ID: id, Action: "get", Guard: dashboardFileGuard(claims)})
	if err != nil {
		return out, err
	}
	if pub.Dashboard == nil || pub.Snapshot == nil || pub.Dashboard.Published != revision {
		return out, fmt.Errorf("use this item's current published revision")
	}
	kind := "dashboard"
	if pub.Snapshot.Artifact {
		kind = "artifact"
	}
	in := teamOnlineRequest{TeamProjectID: source.CrewID, SourceProjectID: source.CrewID, Kind: kind, ItemID: id, Revision: revision, SubmissionID: submission, Preview: true}
	if IsLocalMode() {
		binding, err := crewroot.ReadSync(source.CrewID)
		if err != nil || binding == nil || binding.Side != "local" || binding.ConnectionID == "" {
			return out, fmt.Errorf("join a Team in this Crew first")
		}
		c, err := desktopConnectionFor(claims.UserID, binding.ConnectionID)
		if err != nil {
			return out, err
		}
		in.TeamProjectID = binding.PeerProjectID
		err = crewTeamRemoteJSON(ctx, claims.UserID, c, http.MethodPost, "/api/dashboards", map[string]any{"action": "begin_handoff", "team_project_id": in.TeamProjectID, "source_project_id": in.SourceProjectID, "item_kind": in.Kind, "dashboard_id": id, "revision": revision, "submission_id": submission}, &out)
		return out, err
	}
	return api.beginTeamDashboardHandoff(ctx, in)
}
