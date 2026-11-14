package server

import (
	"context"
	"log"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectinstructions"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

// PLAT-746-1026: the Team agent (the Team's online home Crew) works by the Team Crew's rules and skills. They are read
// from the server's own copy of the Team Crew when a turn starts, never copied into the home and never taken from the
// owner's Mac, so editing them in the Team Crew changes the Team agent at once and it keeps working when the Mac is off.

// teamHomeSource returns the Team Crew whose way of working the Crew at workspacePath follows: only a Team's online
// home, only while the Team is on and its owner is still the Crew's owner and an active account.
func teamHomeSource(workspacePath string) (crewTeam, bool) {
	source, _, _, ok := teamHomeSourceWhy(workspacePath)
	return source, ok
}

// teamHomeSourceWhy is teamHomeSource with the home's project id and, when it says no for a real Team home, why.
func teamHomeSourceWhy(workspacePath string) (source crewTeam, home, why string, ok bool) {
	home = crewPanelProjectForPath(workspacePath)
	if home == "" {
		return crewTeam{}, "", "", false
	}
	homeTeam, err := readCrewTeam(home)
	if err != nil || homeTeam.RosterSource == "" {
		return crewTeam{}, home, "", false // an ordinary Crew: nothing to say
	}
	source, err = readCrewTeam(homeTeam.RosterSource)
	switch {
	case err != nil:
		return crewTeam{}, home, "source_unreadable", false
	case !source.On:
		return crewTeam{}, home, "team_off", false
	case source.OnlineHome != home || homeTeam.OwnerID != "team-"+source.ProjectID:
		return crewTeam{}, home, "not_the_home", false
	}
	if owner := ownerOfCrewProjectID(source.ProjectID); owner == "" || owner != source.OwnerID || !crewTeamAccountActive(owner) {
		return crewTeam{}, home, "owner_not_current", false
	}
	return source, home, "", true
}

// teamHomeSkills are the skills the Team Crew selected, for the Team agent's turns.
func teamHomeSkills(ctx context.Context, workspacePath string) []string {
	source, ok := teamHomeSource(workspacePath)
	if !ok {
		return nil
	}
	names, err := productSelectedSkills(ctx, crewProfileID, crewProjectRootByID(source.ProjectID))
	if err != nil {
		return nil
	}
	return names
}

// teamHomeRules is the Team Crew's own rules (its chat instructions and PROJECT_INSTRUCTIONS.md) as a prompt section
// for the Team agent; empty when there are none.
func (api *StreamingAPI) teamHomeRules(ctx context.Context, workspacePath string) string {
	source, ok := teamHomeSource(workspacePath)
	if !ok {
		return ""
	}
	setup, workspace, ownerCtx, ok := api.crewSetupOf(ctx, source.OwnerID, source.ProjectID)
	if !ok {
		return ""
	}
	var parts []string
	if text := strings.TrimSpace(setup.ChatInstructions); text != "" {
		parts = append(parts, text)
	}
	if text, found, err := readFileFromWorkspace(ownerCtx, agentProfileRuntimeWorkspace(source.OwnerID, workspace)+"/"+projectinstructions.FileName); err == nil && found && strings.TrimSpace(text) != "" {
		parts = append(parts, strings.TrimSpace(text))
	}
	if len(parts) == 0 {
		return ""
	}
	return "## The Team's rules\nThese are the rules of the Team you work for. Follow them as the Team's own.\n\n" + strings.Join(parts, "\n\n")
}

func init() {
	// The Team agent also reads the Team Crew's own skills/ folder, read-only, from the server's copy (the same checks as
	// the rules above; the workspace service links it like a workflow's owning Crew).
	skills.ExtraSkillsRoot = func(workspacePath string) string {
		source, home, why, ok := teamHomeSourceWhy(workspacePath)
		if home != "" && (ok || why != "") {
			logTeamSkillsParent(home, source.ProjectID, ok, why)
		}
		if !ok {
			return ""
		}
		return crewProjectRootByID(source.ProjectID)
	}
}

// teamSkillsParentState remembers what was last logged for each home, so one line shows when the Team's skills folder
// is applied or dropped instead of one per skill read.
var teamSkillsParentState sync.Map

func logTeamSkillsParent(home, source string, applied bool, why string) {
	state := "applied"
	if !applied {
		state = "dropped:" + why
	}
	if previous, loaded := teamSkillsParentState.Swap(home, state); loaded && previous == state {
		return
	}
	if applied {
		log.Printf("[TEAM_WAY] event=skills_parent home=%s source=%s result=applied", home, source)
		return
	}
	log.Printf("[TEAM_WAY] event=skills_parent home=%s result=dropped reason=%s", home, why)
}
