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

func readCrewTeamAll(project string) bool {
	path, err := crewTeamAllPath(project)
	if err != nil {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var state struct {
		AllInTeam bool `json:"all_in_team"`
	}
	return json.Unmarshal(raw, &state) == nil && state.AllInTeam
}

func writeCrewTeamAll(project string, on bool) error {
	path, err := crewTeamAllPath(project)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(path, map[string]bool{"all_in_team": on}, 0o600)
}

// withAllInTeam puts every ready agent (name, purpose and icon) In team; it returns the new list and the names added.
func withAllInTeam(rows []macAgentRow, current []macSharedAgent) ([]macSharedAgent, []string) {
	next := slices.Clone(current)
	added := []string{}
	for _, row := range rows {
		if strings.TrimSpace(row.Purpose) == "" || strings.TrimSpace(row.Icon) == "" {
			continue
		}
		at := slices.IndexFunc(next, func(agent macSharedAgent) bool { return agent.Tab == row.Tab })
		switch {
		case at < 0:
			next = append(next, macSharedAgent{Tab: row.Tab, Audience: "team"})
		case next[at].Audience == "":
			next[at].Audience, next[at].People = "team", nil
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
		} else if next, added := withAllInTeam(rows, readMacSharedAgents(project)); len(added) > 0 {
			if _, _, err = api.pushMacSharedAgents(ctx, claims, project, binding, connection, rows, next); err != nil {
				return nil, err
			}
			out["added"] = added
		}
	}
	log.Printf("[CREW_TEAM] local project=%s event=all_in_team on=%t added=%v", project, on, out["added"])
	return out, nil
}
