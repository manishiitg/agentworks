package server

import (
	"context"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectinstructions"
)

// PLAT-746-1026: the Team agent (the Team's online home Crew) works by the Team Crew's rules and skills. They are read
// from the server's own copy of the Team Crew when a turn starts, never copied into the home and never taken from the
// owner's Mac, so editing them in the Team Crew changes the Team agent at once and it keeps working when the Mac is off.

// teamHomeSource returns the Team Crew whose way of working the Crew at workspacePath follows: only a Team's online
// home, only while the Team is on and its owner is still the Crew's owner and an active account.
func teamHomeSource(workspacePath string) (crewTeam, bool) {
	home := crewPanelProjectForPath(workspacePath)
	if home == "" {
		return crewTeam{}, false
	}
	homeTeam, err := readCrewTeam(home)
	if err != nil || homeTeam.RosterSource == "" {
		return crewTeam{}, false
	}
	source, err := readCrewTeam(homeTeam.RosterSource)
	if err != nil || !source.On || source.OnlineHome != home || homeTeam.OwnerID != "team-"+source.ProjectID {
		return crewTeam{}, false
	}
	if owner := ownerOfCrewProjectID(source.ProjectID); owner == "" || owner != source.OwnerID || !crewTeamAccountActive(owner) {
		return crewTeam{}, false
	}
	return source, true
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
