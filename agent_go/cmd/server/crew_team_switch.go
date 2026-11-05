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
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// The Team tab's on/off switch (owner, 4 Nov, PLAT-856-531): on, every agent ready to share is shared with the team
// and, when no shared folder is chosen, an empty team/ folder in the Crew becomes it; off, only the owner is in the
// team and no agent is shared with the team or chosen people. Public on AgentBridge belongs to Mac online, not
// Team, and stays (the decisions log, 4 Nov). The roster stays saved either way. The Mac decides and does it; the screen and the
// Crew's chat (manage_crew_sync action=team) call the same code.

const crewTeamDefaultFolder = "team"

type crewTeamSwitchResult struct {
	Team          crewTeam `json:"team"`
	Shared        []string `json:"shared,omitempty"`
	NeedsPurpose  []string `json:"needs_purpose,omitempty"`
	Unshared      int      `json:"unshared,omitempty"`
	FolderCreated bool     `json:"folder_created,omitempty"`
	Note          string   `json:"note,omitempty"`
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
	if on && team.Folder.Path == "" {
		dir := filepath.Join(roots.Files, crewTeamDefaultFolder)
		info, statErr := os.Lstat(dir)
		switch {
		case os.IsNotExist(statErr):
			if err = os.MkdirAll(dir, 0o755); err != nil {
				return out, fmt.Errorf("the team folder could not be made: %w", err)
			}
			out.FolderCreated = true
		case statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0:
			return out, fmt.Errorf("this Crew has a %q that is not a folder; choose the shared folder yourself", crewTeamDefaultFolder)
		}
		team.Folder = crewTeamFolder{Path: crewTeamDefaultFolder}
	}
	team.On = on
	if _, manifest, ok := api.resolveExternalWorkspace(ctx, claims, project); ok {
		team.Name = firstNonEmptyTrimmed(manifest.Title, manifest.Label)
	}
	var saved crewTeam
	if err = crewTeamRemoteJSON(ctx, claims.UserID, connection, http.MethodPut, route, team, &saved); err != nil {
		return out, err
	}
	out.Team = saved

	current := readMacSharedAgents(project)
	next := slices.Clone(current)
	if on {
		rows, rowsErr := api.macAgentRows(ctx, claims, project)
		if rowsErr != nil {
			return out, rowsErr
		}
		for _, row := range rows {
			if slices.ContainsFunc(current, func(agent macSharedAgent) bool { return agent.Tab == row.Tab }) {
				continue
			}
			if strings.TrimSpace(row.Purpose) == "" || strings.TrimSpace(row.Icon) == "" {
				out.NeedsPurpose = append(out.NeedsPurpose, row.Name)
				continue
			}
			next = append(next, macSharedAgent{Tab: row.Tab, Audience: "team"})
			out.Shared = append(out.Shared, row.Name)
		}
		if len(out.Shared) > 0 {
			if binding.Mode != "relay" || binding.Editing != "local" {
				out.Note = "Turn on Use from phone and web to share the agents."
				out.Shared = nil
			} else if _, _, err = api.pushMacSharedAgents(ctx, claims, project, binding, connection, rows, next); err != nil {
				return out, err
			}
		}
	} else if binding.Mode == "relay" && binding.Editing == "local" {
		// Public on AgentBridge stays; every people audience goes (owner, 5 Nov: Public is AgentBridge only).
		public := []macSharedAgent{}
		for _, agent := range current {
			if agent.AgentBridgePublic {
				agent.Audience, agent.People = "", nil
				public = append(public, agent)
			}
		}
		if len(public) < len(current) || slices.ContainsFunc(current, func(a macSharedAgent) bool { return a.AgentBridgePublic && a.Audience != "" }) {
			rows, rowsErr := api.macAgentRows(ctx, claims, project)
			if rowsErr != nil {
				return out, rowsErr
			}
			if _, _, err = api.pushMacSharedAgents(ctx, claims, project, binding, connection, rows, public); err != nil {
				return out, err
			}
			out.Unshared = len(current) - len(public)
		}
	}
	// Team on starts with "All agents in team" and Main In team (a fresh start clears an earlier Only me for Main);
	// off ends both (crew_team_all.go).
	if err := writeCrewTeamState(project, func(s *crewTeamLocalState) { s.AllInTeam, s.TeamOn = on, on; s.MainOnlyMe = s.MainOnlyMe && !on }); err != nil {
		log.Printf("[CREW_TEAM] local project=%s all_in_team not saved: %v", project, err)
	}
	log.Printf("[CREW_TEAM] local project=%s server_project=%s event=switch on=%t people=%d folder=%q folder_created=%t shared=%d needs_purpose=%d unshared=%d",
		project, binding.PeerProjectID, on, len(saved.People), saved.Folder.Path, out.FolderCreated, len(out.Shared), len(out.NeedsPurpose), out.Unshared)
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
	if claims == nil || ownerOfCrewProjectID(mux.Vars(r)["project"]) != claims.UserID {
		externalError(w, 403, "team_owner_required", "Only this Crew's owner can turn its Team on or off.")
		return
	}
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
