package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// "All agents in team" (owner, 5 Nov, "A Team is a shared room"): while it is on, every agent of this Mac Crew that
// has a purpose and an icon is In team, including ones made or named later. In team is the agent's audience "team";
// Only me is no audience (an agent public on AgentBridge keeps its entry for that, Mac online's choice).

func crewTeamAllPath(project string) (string, error) {
	if project == "" || strings.ContainsAny(project, `/\`) || project == "." || project == ".." {
		return "", fmt.Errorf("invalid Crew identity")
	}
	state, err := crewroot.StateRoot()
	return filepath.Join(state, "crew-sync", project+"-team.json"), err
}

// crewTeamLocalState is this Mac's own memory of its Crew's Team: All agents in team, whether the Team is on (from the
// last switch or read of it), and an explicit Only me for Main, so Main In team by default never overrides it.
type crewTeamLocalState struct {
	AllInTeam  bool `json:"all_in_team"`
	TeamOn     bool `json:"team_on,omitempty"`
	MainOnlyMe bool `json:"main_only_me,omitempty"`
	// PreviousProject is the online copy this Crew had before Mac online was turned off; turning it on again moves
	// the Team to the new copy.
	PreviousProject string `json:"previous_online_project,omitempty"`
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

func readCrewTeamAll(project string) bool { return readCrewTeamState(project).AllInTeam }

func writeCrewTeamAll(project string, on bool) error {
	return writeCrewTeamState(project, func(s *crewTeamLocalState) { s.AllInTeam = on })
}

// withTeamMain puts Main In team while the Team is on (owner, 5 Nov: "main agents shared by default"), unless the owner
// chose Only me for it.
func withTeamMain(project string, current []macSharedAgent) ([]macSharedAgent, bool) {
	state := readCrewTeamState(project)
	at := slices.IndexFunc(current, func(agent macSharedAgent) bool { return agent.Tab == macMainTab })
	if !state.TeamOn || state.MainOnlyMe || at >= 0 && current[at].Audience == "team" {
		return current, false
	}
	next := slices.Clone(current)
	if at < 0 {
		next = append(next, macSharedAgent{Tab: macMainTab, Audience: "team"})
	} else {
		next[at].Audience = "team"
	}
	return next, true
}

// noteMainChoice records the owner's own In team / Only me choice for Main from a saved share list.
func noteMainChoice(project string, saved []macSharedAgent) {
	inTeam := slices.ContainsFunc(saved, func(agent macSharedAgent) bool { return agent.Tab == macMainTab && crewTeamInTeam(agent) })
	if err := writeCrewTeamState(project, func(s *crewTeamLocalState) { s.MainOnlyMe = !inTeam }); err != nil {
		log.Printf("[CREW_TEAM] local project=%s main choice not saved: %v", project, err)
	}
}

// withAllInTeam puts every ready agent (name, purpose and icon) In team, Main too unless the owner chose Only me for
// it; it returns the new list and the names added.
func withAllInTeam(rows []macAgentRow, current []macSharedAgent, mainOnlyMe bool) ([]macSharedAgent, []string) {
	next := slices.Clone(current)
	added := []string{}
	for _, row := range rows {
		if strings.TrimSpace(row.Purpose) == "" || strings.TrimSpace(row.Icon) == "" || row.Tab == macMainTab && mainOnlyMe {
			continue
		}
		at := slices.IndexFunc(next, func(agent macSharedAgent) bool { return agent.Tab == row.Tab })
		switch {
		case at < 0:
			next = append(next, macSharedAgent{Tab: row.Tab, Audience: "team"})
		case next[at].Audience == "":
			next[at].Audience = "team"
		default:
			continue
		}
		added = append(added, row.Name)
	}
	return next, added
}

// setCrewTeamAll saves the choice and, when on, puts every ready agent In team now.
func (api *StreamingAPI) setCrewTeamAll(ctx context.Context, claims *UserClaims, project string, on bool) (map[string]any, error) {
	if err := writeCrewTeamAll(project, on); err != nil {
		return nil, err
	}
	out := map[string]any{"all_in_team": on}
	if on {
		_, binding, connection, err := api.desktopCrewChatBinding(ctx, project)
		if err != nil || binding == nil || binding.Mode != "relay" || binding.Editing != "local" {
			out["note"] = "Turn on Use from phone and web to put the agents in the team."
		} else if rows, err := api.macAgentRows(ctx, claims, project); err != nil {
			return nil, err
		} else if next, added := withAllInTeam(rows, readMacSharedAgents(project), readCrewTeamState(project).MainOnlyMe); len(added) > 0 {
			if _, _, err = api.pushMacSharedAgents(ctx, claims, project, binding, connection, rows, next); err != nil {
				return nil, err
			}
			out["added"] = added
		}
	}
	log.Printf("[CREW_TEAM] local project=%s event=all_in_team on=%t added=%v", project, on, out["added"])
	return out, nil
}
