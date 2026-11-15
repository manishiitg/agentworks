package server

import "context"

// crewTeamHomeAgent is the Team agent (the Team's online home Main, PLAT-746-1022) as the right panel's Agents list
// shows it to a current member: always online, answering when the others are away. Its last line comes from the home
// Main through the same current ChatShare filter as every other agent.
type crewTeamHomeAgent struct {
	Activity string `json:"activity,omitempty"`
	Name     string `json:"name"`
	Line     string `json:"line"`
	Address  string `json:"address,omitempty"`
	crewTeamMessagePreview
}

func crewTeamHomeAgentFor(ctx context.Context, t crewTeam, viewer string) *crewTeamHomeAgent {
	if !t.On || t.OnlineHome == "" || t.RosterSource != "" || !t.member(viewer) || !crewTeamAccountActive(viewer) {
		return nil
	}

	home, err := readCrewTeam(t.OnlineHome)
	if err != nil || !home.On || home.RosterSource != t.ProjectID || !home.member(viewer) || !crewTeamAccountActive(home.OwnerID) {
		return nil
	}
	key := crewOwnerMainChatKey(t.OnlineHome)
	registered, ok := home.registeredAgent(home.OwnerID, key)
	if !ok || !registered.InTeam {
		return nil
	}
	out := &crewTeamHomeAgent{Name: firstNonEmptyTrimmed(registered.Name, "Team agent"), Line: "Always online · set up Team automations and how the team works", Address: crewTeamAgentAddress(t.OnlineHome, registered.ID)}
	preview := crewTeamAgentMessagePreviews(ctx, home, crewTeamActivityPostsFor(home, viewer))
	for _, agent := range memberTeamAgentsWithMain(ctx, home, home.OwnerID, viewer, key) {
		if agent.ChatKey == key {
			out.crewTeamMessagePreview = preview(home.OwnerID, agent)
			out.Activity = agent.Activity
			break
		}
	}
	return out
}

// The right panel does not maintain a second audience: decorate only rows from
// the same reach answer used by bridge discovery and message admission.
type crewTeamPanelReach struct {
	crewTeamPanelRuntime
	crewTeamReachableAgent
	crewTeamMessagePreview
}

func crewTeamPanelReachFor(ctx context.Context, t crewTeam, viewer string, people []crewTeamAgentsPerson) []crewTeamPanelReach {
	out := []crewTeamPanelReach{}
	if !crewTeamReachOpen(t, viewer) {
		return out
	}
	preview := crewTeamAgentMessagePreviews(ctx, t, crewTeamActivityPostsFor(t, viewer))
	byID := map[string]crewTeamAgent{}
	for _, person := range people {
		for _, agent := range person.Agents {
			byID[agent.ID] = agent
		}
	}
	for _, row := range crewTeamReachableIn(ctx, t, viewer) {
		projected := crewTeamPanelReach{crewTeamReachableAgent: row}
		if row.Kind == "team_agent" {
			if home := crewTeamHomeAgentFor(ctx, t, viewer); home != nil {
				projected.crewTeamMessagePreview = home.crewTeamMessagePreview
				projected.Name = home.Name
				projected.Activity = home.Activity
			}
		} else if agent, ok := byID[row.ID]; ok {
			projected.crewTeamMessagePreview = preview(row.PersonID, agent)
		}
		out = append(out, projected)
	}
	return out
}
