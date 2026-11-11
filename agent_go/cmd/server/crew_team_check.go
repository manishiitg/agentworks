package server

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/spf13/cobra"
)

func init() {
	ServerCmd.AddCommand(&cobra.Command{
		Use: "team-check", Short: "Check saved Team registries without starting the server", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return checkCrewTeamRegistries(cmd.OutOrStdout()) },
	})
}

// Uses the new runtime's actual reader. No discovery, writes, agents or services.
// AGENTWORKS_STATE_ROOT selects the operator's live or converted scratch state.
func checkCrewTeamRegistries(out io.Writer) error {
	state, err := crewroot.StateRoot()
	if err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(state, "team-folders", "*", "team.json"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no Team records found; check AGENTWORKS_STATE_ROOT")
	}
	failed := 0
	for _, file := range files {
		project := filepath.Base(filepath.Dir(file))
		t, readErr := readCrewTeam(project)
		result := map[string]any{"team_id": project, "result": "stop"}
		valid := readErr == nil
		joined, invited, shared, rules := 0, 0, 0, 0
		people := map[string]bool{t.OwnerID: true}
		for _, p := range t.People {
			people[p.UserID] = true
			if p.Invited {
				invited++
			} else {
				joined++
			}
		}
		bindings := map[string]bool{}
		for id, a := range t.Agents {
			_, idErr := uuid.Parse(strings.TrimPrefix(id, crewTeamAgentIDPrefix))
			binding := a.OwnerID + "/" + a.Tab
			if idErr != nil || !strings.HasPrefix(id, crewTeamAgentIDPrefix) || id != a.ID || !people[a.OwnerID] || a.OwnerID == "" || a.Tab == "" || a.ChatKey == "" || bindings[binding] || (a.Place != "mac" && a.Place != "server") {
				valid = false
			}
			bindings[binding] = true
			if a.InTeam {
				shared++
			}
			if a.Rule != (crewTeamAgentRule{}) {
				rules++
			}
			if a.Rule.ChatShare != crewChatShareLive && a.Rule.ChatShare != crewChatShareAgent {
				valid = false
			}
		}
		if valid {
			result["result"], result["owner"] = "ok", t.OwnerID
			result["people"] = map[string]int{"joined": joined, "invited": invited}
			result["agents"] = map[string]int{"total": len(t.Agents), "in_team": shared, "rules": rules}
		} else {
			failed++
			result["reason"] = "Team record or agent registry is invalid"
		}
		if err := json.NewEncoder(out).Encode(result); err != nil {
			return err
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d Team registries failed validation", failed)
	}
	return nil
}
