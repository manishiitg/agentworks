package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Sharing all of one's agents in an online project at once, and how one's new agents there start (owner, 26 Nov):
// "can we easy toggle to like make all of them whole team" and "there should be a default settings for new agents".
// Both are the maker's own, per online project and Team, and never touch anyone else's agents. A new agent still
// starts as Only me unless its maker chose Whole Team here.

var errCrewTeamOnlineAgentsChoice = errors.New("choose only_me or whole_team")

func crewTeamOnlineAgentsChoice(value string) bool {
	return value == "" || value == "only_me" || value == crewTeamAudienceWholeTeam
}

// setCrewTeamOnlineAgentsSharing saves the maker's default for new agents (newAgents) and/or shares every agent they
// made in the online project with the whole Team or nobody (allNow); "" leaves that part as it is. It returns how many
// agents changed.
func setCrewTeamOnlineAgentsSharing(ctx context.Context, team, project, maker, newAgents, allNow string) (int, error) {
	if !crewTeamOnlineAgentsChoice(newAgents) || !crewTeamOnlineAgentsChoice(allNow) || newAgents == "" && allNow == "" {
		return 0, errCrewTeamOnlineAgentsChoice
	}
	dir, err := crewTeamDir(team)
	if err != nil {
		return 0, err
	}
	unlock, err := wf.LockWorkspace(ctx, dir)
	if err != nil {
		return 0, err
	}
	defer unlock()
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	t, err := readCrewTeam(team)
	if err != nil || ownerOfCrewProjectID(team) != t.OwnerID || !crewTeamProjectMakerAllowed(t, project, maker) || !crewTeamAccountActive(maker) {
		return 0, errCrewTeamAgentNotYours
	}
	owner := crewTeamProjectMakerID(project, maker)
	if newAgents == crewTeamAudienceWholeTeam {
		if t.AgentDefaults == nil {
			t.AgentDefaults = map[string]string{}
		}
		t.AgentDefaults[owner] = crewTeamAudienceWholeTeam
	} else if newAgents == "only_me" {
		delete(t.AgentDefaults, owner)
	}
	var changed []string
	if allNow != "" {
		share := allNow == crewTeamAudienceWholeTeam
		for id, a := range t.Agents {
			if a.OwnerID != owner || a.InTeam == share && (!share || a.Rule.Audience == "") {
				continue
			}
			a.InTeam = share
			if share {
				a.Rule.Audience, a.Rule.People = "", nil
			}
			t.Agents[id] = a
			changed = append(changed, id)
		}
	}
	t.Revision++
	if err = fsutil.WriteJSONAtomic(filepath.Join(dir, "team.json"), t, 0600); err != nil {
		return 0, err
	}
	publishCrewPanelChanged(team)
	go pushCrewTeamRecordChanged(t)
	for _, id := range changed {
		defer noteCrewTeamAgentChanged(team, owner, id)
	}
	crewMacTeamTabsCache.Delete(team + "\x00" + owner)
	crewMacTeamTabsCache.Delete(team + "\x00" + maker)
	log.Printf("[CREW_TEAM] project=%s event=online_agents_sharing online_project=%s user=%s new_agents=%q all_now=%q changed=%d revision=%d", team, project, logUsernameForUserID(maker), newAgents, allNow, len(changed), t.Revision)
	return len(changed), nil
}

// handleCrewTeamOnlineAgents is POST /crew-teams/{project}/online-agents {project_id, new_agents?, all_now?}.
func handleCrewTeamOnlineAgents(w http.ResponseWriter, r *http.Request, team, user string) {
	var in struct {
		ProjectID string `json:"project_id"`
		NewAgents string `json:"new_agents"`
		AllNow    string `json:"all_now"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in) != nil || in.ProjectID == "" {
		externalError(w, 400, "invalid_choice", "Choose the online project and Only me or Whole Team.")
		return
	}
	n, err := setCrewTeamOnlineAgentsSharing(r.Context(), team, in.ProjectID, user, in.NewAgents, in.AllNow)
	switch {
	case errors.Is(err, errCrewTeamOnlineAgentsChoice):
		externalError(w, 400, "invalid_choice", "Choose Only me or Whole Team.")
	case errors.Is(err, errCrewTeamAgentNotYours):
		externalError(w, 403, "not_in_project", "Only people who work in that online project share the agents they made there.")
	case err != nil:
		externalError(w, 409, "agent_update_failed", "It was not saved. Try again.")
	default:
		externalJSON(w, map[string]any{"changed": n})
	}
}
