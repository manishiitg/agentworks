package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	"github.com/manishiitg/coding-agent-loop/workspace/teamonline"
)

type teamDashboardStart struct {
	ProjectID string `json:"project_id"`
	Workspace string `json:"workspace"`
	Prompt    string `json:"prompt,omitempty"`
	Message   string `json:"message,omitempty"`
}

// Both the button and dashboard tool enter here. The browser cannot nominate a
// destination, copy dependencies, or decide that a live page is static.
func (api *StreamingAPI) addDashboardToTeam(ctx context.Context, root, id string) (teamDashboardStart, error) {
	out := teamDashboardStart{}
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.Scope != "" && (claims.AccessToken == nil || !claims.AccessToken.Allows("crews:write")) {
		return out, fmt.Errorf("use your own Builder connection to add this item")
	}
	target, err := api.dashboardTarget(ctx, claims, root)
	if err != nil || target.Kind != "crew" || !dashboardAuthoringAllowed(claims, &target) {
		return out, fmt.Errorf("add an item from your own Crew")
	}
	source, err := resolveTriggerTarget(ctx, claims, "workspace:work:"+target.ID)
	if err != nil || source.CrewOwner != claims.UserID || !crewTeamClaims(claims, source.CrewID, true) {
		return out, fmt.Errorf("add an item from your own Crew")
	}
	current, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: target.Root, ID: id, Action: "get", Guard: dashboardFileGuard(claims)})
	if err != nil {
		return out, err
	}
	if current.Dashboard == nil || current.Snapshot == nil || current.Dashboard.Published == "" {
		return out, fmt.Errorf("publish this item before adding it to the Team")
	}
	teamProject := source.CrewID
	var connection *desktopConnection
	var team crewTeam
	if IsLocalMode() {
		binding, err := crewroot.ReadSync(source.CrewID)
		if err != nil || binding == nil || binding.Side != "local" || binding.ConnectionID == "" {
			return out, fmt.Errorf("join a Team in this Crew first")
		}
		c, err := desktopConnectionFor(claims.UserID, binding.ConnectionID)
		if err != nil {
			return out, fmt.Errorf("connect to your Team's server and try again")
		}
		connection = &c
		teamProject = binding.PeerProjectID
		if err = crewTeamRemoteJSON(ctx, claims.UserID, c, http.MethodGet, "/api/crew-teams/"+teamProject, nil, &team); err != nil {
			return out, err
		}
		if !team.On || !team.CanManage {
			return out, fmt.Errorf("join this Team before adding work to it")
		}
	} else {
		team, err = teamOnlineHome(ctx, teamProject)
		if err != nil {
			return out, err
		}
	}
	out.ProjectID, out.Workspace = source.CrewID, target.Root
	static := teamDashboardStaticArtifact(*current.Snapshot)
	kind := "dashboard"
	if current.Snapshot.Artifact {
		kind = "artifact"
	}
	input := teamOnlineRequest{TeamProjectID: teamProject, SourceProjectID: source.CrewID, Kind: kind, ItemID: id, Revision: current.Dashboard.Published, SubmissionID: uuid.NewString(), Preview: true}
	if static {
		args := map[string]any{"source_project_id": input.SourceProjectID, "kind": input.Kind, "item_id": input.ItemID, "item_revision": input.Revision, "submission_id": input.SubmissionID, "preview": true}
		if _, err = api.teamOnlineChat(ctx, claims, connection, teamProject, args); err != nil {
			return out, err
		}
		args["preview"] = false
		if _, err = api.teamOnlineChat(ctx, claims, connection, teamProject, args); err != nil {
			return out, err
		}
		out.Message = current.Dashboard.Title + " is on the Team."
		log.Printf("[TEAM_ONLINE] event=artifact_added person=%s source=%s team=%s item=%s", claims.UserID, source.CrewID, teamProject, id)
		return out, nil
	}
	out.Prompt = fmt.Sprintf("Add %q (published item %q) to my Team %q. Follow the Add to Team dashboard guide and agree with the Team agent over AgentBridge before preview/apply. Ask me only if something you need is missing, and give me a plain result.", current.Dashboard.Title, id, team.Name)
	log.Printf("[TEAM_ONLINE] event=agent_handoff_prepared person=%s source=%s team=%s item=%s", claims.UserID, source.CrewID, teamProject, id)
	return out, nil
}

// An Artifact with declared live inputs is not the no-conversation fast path.
func teamDashboardStaticArtifact(snapshot dashboards.Snapshot) bool {
	deps := teamonline.Dependencies{}
	if declaration, ok := snapshot.Files["online.json"]; ok {
		decoder := json.NewDecoder(strings.NewReader(declaration))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&deps) != nil || decoder.Decode(new(any)) != io.EOF {
			// The source agent can prepare an incomplete manifest. Never take the
			// direct-copy path by ignoring a malformed or unknown live dependency.
			return false
		}
	}
	static := snapshot.Artifact && !snapshot.Motion && len(deps.Dataset.Tables) == 0 && len(deps.Dataset.Views) == 0 && len(deps.Dataset.Triggers) == 0 && len(deps.Functions) == 0 && len(deps.Automations) == 0
	for name := range snapshot.Files {
		if strings.HasPrefix(name, "scripts/") {
			static = false
		}
	}
	return static
}
