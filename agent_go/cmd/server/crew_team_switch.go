package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// The Team tab's on/off switch from the Mac that holds the Crew (owner, 4-5 Nov, PLAT-856-531). The switch is the
// server's Team record like every Team setting: on starts with All agents in team and, with nothing shared yet, the
// team/ folder; off leaves only the owner in the team, the roster saved. The website, members and MCP switch it on the
// server directly; this Mac path also turns Mac online on first (Team works through it) and applies the record here at
// once (crew_team_apply.go). Public on AgentBridge belongs to Mac online, not Team, and stays.

const crewTeamDefaultFolder = "team"

type crewTeamSwitchResult struct {
	Team          crewTeam `json:"team"`
	FolderCreated bool     `json:"folder_created,omitempty"`
}

func (api *StreamingAPI) switchCrewTeam(ctx context.Context, claims *UserClaims, project string, on bool, connectionHint string) (crewTeamSwitchResult, error) {
	var out crewTeamSwitchResult
	roots, err := api.crewSyncRoots(ctx, claims, project)
	if err != nil {
		return out, err
	}
	binding, err := crewroot.ReadSync(roots.ID)
	if err == nil && binding == nil && on {
		// Team works through Mac online's connection (owner, 5 Nov): Team on turns Mac online on, no extra step.
		if err = api.turnMacOnlineOnForTeam(ctx, claims, roots.ID, connectionHint); err != nil {
			return out, err
		}
		binding, err = crewroot.ReadSync(roots.ID)
		if binding != nil {
			log.Printf("[CREW_TEAM] local project=%s server_project=%s event=online_tied mac_online=on team=on", roots.ID, binding.PeerProjectID)
		}
	}
	if err == nil && binding == nil && !on {
		return out, nil // Mac online is off, so the Team already is
	}
	if err != nil || binding == nil || binding.Side != "local" {
		return out, fmt.Errorf("Turn on Use from phone and web for this Crew first.")
	}
	connection, err := desktopConnectionFor(claims.UserID, binding.ConnectionID)
	if err != nil {
		return out, err
	}
	route := "/api/crew-teams/" + url.PathEscape(binding.PeerProjectID)
	var team crewTeam
	if err = crewTeamRemoteJSON(ctx, claims.UserID, connection, http.MethodGet, route, nil, &team); err != nil {
		return out, err
	}
	team.On = on
	if _, manifest, ok := api.resolveExternalWorkspace(ctx, claims, project); ok {
		team.Name = firstNonEmptyTrimmed(manifest.Title, manifest.Label)
	}
	if err = crewTeamRemoteJSON(ctx, claims.UserID, connection, http.MethodPut, route, team, &out.Team); err != nil {
		return out, err
	}
	out.FolderCreated = api.applyCrewTeamOnMac(ctx, roots.ID, out.Team)
	log.Printf("[CREW_TEAM] local project=%s server_project=%s event=switch on=%t people=%d folder=%q folder_created=%t",
		project, binding.PeerProjectID, on, len(out.Team.People), out.Team.Folder.display(), out.FolderCreated)
	return out, nil
}

// turnMacOnlineOnForTeam turns on Mac online through the given connection, or the only saved one, with the same code
// as Mac online's own switch.
func (api *StreamingAPI) turnMacOnlineOnForTeam(ctx context.Context, claims *UserClaims, project, connection string) error {
	if connection == "" {
		saved, err := readDesktopConnections(claims.UserID)
		if err != nil || len(saved) == 0 {
			return fmt.Errorf("Sign in to a network in the AgentWorks app first.")
		}
		if len(saved) > 1 {
			names := []string{}
			for _, item := range saved {
				names = append(names, desktopConnectionLabel(item))
			}
			return fmt.Errorf("Which network should the team use? %s. Turn on Mac online with that network, then the Team.", strings.Join(names, ", "))
		}
		connection = saved[0].ID
	}
	body, _ := json.Marshal(map[string]string{"action": "relay"})
	request, err := http.NewRequestWithContext(context.WithValue(ctx, UserContextKey, claims), http.MethodPost, "http://localhost/api/desktop-connections/"+url.PathEscape(connection)+"/crew-sync/"+url.PathEscape(project), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request = mux.SetURLVars(request, map[string]string{"project": project, "connection": connection})
	capture := &internalResponseCapture{}
	api.runDesktopCrewSync(capture, request, false)
	if capture.status >= 300 {
		var failure map[string]any
		_ = json.Unmarshal(capture.body.Bytes(), &failure)
		return fmt.Errorf("%s", externalErrorMessage(failure, "Mac online could not be turned on. Try again."))
	}
	return nil
}

// handleDesktopCrewTeamSwitch is the Team tab's switch on the Mac.
func (api *StreamingAPI) handleDesktopCrewTeamSwitch(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	var input struct {
		On bool `json:"on"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&input) != nil {
		externalError(w, 400, "invalid_request", "Turn Team on or off.")
		return
	}
	claims := GetUserFromContext(r.Context())
	// With Mac online off, Team on turns it on through this connection; a Crew online elsewhere is refused.
	if b, e := crewroot.ReadSync(mux.Vars(r)["project"]); e != nil || b != nil && b.ConnectionID != mux.Vars(r)["connection"] {
		externalError(w, 409, "team_connection_required", "Turn on this Crew's connection to your company first.")
		return
	}
	out, err := api.switchCrewTeam(r.Context(), claims, mux.Vars(r)["project"], input.On, mux.Vars(r)["connection"])
	if err != nil {
		externalError(w, 409, "team_switch_failed", err.Error())
		return
	}
	externalJSON(w, out)
}
