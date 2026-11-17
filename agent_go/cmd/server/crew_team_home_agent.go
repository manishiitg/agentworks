package server

import (
	"context"

	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

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
	// Decoration only: callers pass rows already admitted by crewTeamOnlineCopyAdmits (owner always, even Team off).
	if t.OnlineHome == "" || t.RosterSource != "" || !crewTeamAccountActive(viewer) {
		return nil
	}
	home, err := readCrewTeam(t.OnlineHome)
	if err != nil || home.RosterSource != t.ProjectID || !crewTeamAccountActive(home.OwnerID) {
		return nil
	}
	key := crewOwnerMainChatKey(t.OnlineHome)
	registered, ok := home.registeredAgent(home.OwnerID, key)
	if !ok || !crewTeamOnlineCopyAdmits(t, viewer, registered) {
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
	if !crewTeamReachOpen(t, viewer) && !crewTeamOwnerReach(t, viewer) {
		return out
	}
	preview := crewTeamAgentMessagePreviews(ctx, t, crewTeamActivityPostsFor(t, viewer))
	byID := map[string]crewTeamAgent{}
	for _, person := range people {
		for _, agent := range person.Agents {
			byID[agent.ID] = agent
		}
	}
	for _, row := range crewTeamReachableFrom(ctx, t, viewer, people) {
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

// crewTeamAgentStatus is the Team agent's pinned row in the right Team panel: whether this viewer can reach it now,
// why not, and how it works, in the server's words. It reads state only; it never creates the home.
type crewTeamAgentStatus struct {
	State   string   `json:"state"` // ready, setting_up, off or unavailable
	Line    string   `json:"line"`
	How     []string `json:"how"`
	Address string   `json:"address,omitempty"`
	Program string   `json:"program,omitempty"` // the AI program it runs on (Claude Code, Codex, ...)
}

var crewTeamAgentHow = []string{
	"Always online on the Team's server, even when everyone's Mac is off.",
	"Runs the Team's schedules and Watchers, and anything added to the Team.",
	"Answers teammates when an agent's owner is away and that agent has no online copy.",
	"Works as the Team, with the Team's AI accounts and keys, never a member's own login.",
	"Anyone on the Team can ask it.",
}

func crewTeamAgentStatusFor(t crewTeam, viewer string, reachable []crewTeamPanelReach) *crewTeamAgentStatus {
	if t.RosterSource != "" {
		return nil // the online home itself
	}
	out := &crewTeamAgentStatus{How: crewTeamAgentHow}
	switch {
	case !t.On:
		out.State, out.Line = "off", "Turn the Team on to start the Team agent."
		return out
	case t.OnlineHome == "":
		out.State, out.Line = "setting_up", "Being set up on the server. Check again in a minute."
		return out
	}
	home, err := readCrewTeam(t.OnlineHome)
	if err != nil || home.RosterSource != t.ProjectID || !crewTeamAccountActive(home.OwnerID) {
		out.State, out.Line = "unavailable", "Its online home can't be opened. Ask the Team owner to turn the Team off and on again."
		return out
	}
	if cliHostOS == "linux" && slots.Enabled() {
		if slot, enabled, err := slots.For(home.OwnerID); err != nil || !enabled || slot == "" {
			out.State, out.Line = "unavailable", "This server has no account slot for it yet. Ask the server admin to add one."
			return out
		}
	}
	for _, row := range reachable {
		if row.Kind == "team_agent" {
			out.State, out.Line, out.Address = "ready", "Always online. Ask it about Team schedules, Watchers and how the Team works.", row.Address
			return out
		}
	}
	out.State, out.Line = "unavailable", "You can't reach it right now. Ask the Team owner to check that you're on the Team."
	return out
}
