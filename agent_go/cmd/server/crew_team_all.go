package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// The owner's Mac and its Crew's Team (owner, 5 Nov: the server decides, one Team tab everywhere). Every Team setting
// is the company server's Team record: on/off, people, what is shared, All agents in team and each agent's In team /
// Only me. This Mac only applies it (crew_team_apply.go): it lists its agents on the online copy while the Team is
// on, and makes the team/ folder the server chose. What stays per Mac is Mac online and its own folders.

func crewTeamAllPath(project string) (string, error) {
	if project == "" || strings.ContainsAny(project, `/\`) || project == "." || project == ".." {
		return "", fmt.Errorf("invalid Crew identity")
	}
	state, err := crewroot.StateRoot()
	return filepath.Join(state, "crew-sync", project+"-team.json"), err
}

// crewTeamLocalState is this Mac's own memory of its Crew's Team.
type crewTeamLocalState struct {
	// TeamOn is the server record's switch as this Mac last applied it.
	TeamOn bool `json:"team_on,omitempty"`
	// PreviousProject is the online copy this Crew had before Mac online was turned off; turning it on again moves
	// the Team to the new copy.
	PreviousProject string `json:"previous_online_project,omitempty"`
	// MemberOf: this Mac Crew is a Team member's working copy of someone else's Crew (crew_team_where.go), paired to
	// that Crew's online copy as the member. It is never repaired into an online copy of its own.
	MemberOf string `json:"member_of,omitempty"`
}

func readCrewTeamState(project string) crewTeamLocalState {
	var state crewTeamLocalState
	if path, err := crewTeamAllPath(project); err == nil {
		if raw, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(raw, &state)
		}
	}
	return state
}

func writeCrewTeamState(project string, change func(*crewTeamLocalState)) error {
	path, err := crewTeamAllPath(project)
	if err != nil {
		return err
	}
	state := readCrewTeamState(project)
	before := state
	change(&state)
	if state == before {
		return nil
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(path, state, 0o600)
}

// crewTeamRoster is this Mac Crew's list on its online copy for the Team's switch: on, every agent is listed for the
// Team (audience "team"), keeping each entry's other choices, and who sees which is the server's In team / Only me;
// off, only agents public on AgentBridge stay, with no people audience. It says whether the list changed.
func crewTeamRoster(rows []macAgentRow, current []macSharedAgent, on bool) ([]macSharedAgent, bool) {
	next := slices.Clone(current)
	changed := false
	if !on {
		next = slices.DeleteFunc(next, func(agent macSharedAgent) bool { return !agent.AgentBridgePublic })
		changed = len(next) < len(current)
		for i := range next {
			changed = changed || next[i].Audience != ""
			next[i].Audience = ""
		}
		return next, changed
	}
	for _, row := range rows {
		at := slices.IndexFunc(next, func(agent macSharedAgent) bool { return agent.Tab == row.Tab })
		switch {
		case at < 0:
			next = append(next, macSharedAgent{Tab: row.Tab, Audience: "team"})
		case next[at].Audience == "":
			next[at].Audience = "team"
		default:
			continue
		}
		changed = true
	}
	return next, changed
}

// setCrewTeamAll is the owner's own All my agents in team from this Mac (its screen and its chat): the server's record.
func (api *StreamingAPI) setCrewTeamAll(ctx context.Context, project string, on bool) (map[string]any, error) {
	claims, binding, connection, err := api.desktopCrewChatBinding(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("Turn on Mac online for this Crew first.")
	}
	body := map[string]any{"agent_key": crewTeamAllMine, "in_team": on}
	if err = crewTeamRemoteJSON(ctx, claims.UserID, connection, http.MethodPost, "/api/crew-teams/"+url.PathEscape(binding.PeerProjectID)+"/agent-visibility", body, nil); err != nil {
		return nil, err
	}
	log.Printf("[CREW_TEAM] local project=%s event=all_in_team on=%t", project, on)
	return map[string]any{"all_in_team": on}, nil
}
