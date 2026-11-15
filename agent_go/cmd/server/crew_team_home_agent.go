package server

import (
	"context"
	"strings"
)

// crewTeamHomeAgent is the Team agent (the Team's online home Main, PLAT-746-1022) as the right panel's Agents list
// shows it to a current member: always online, answering when the others are away. Its last line comes from the home
// Main through the same current ChatShare filter as every other agent.
type crewTeamHomeAgent struct {
	Activity string `json:"activity,omitempty"`
	Name     string `json:"name"`
	Line     string `json:"line"`
	Address  string `json:"address,omitempty"`
	// WatchKey is its chat in the home: the Mac opens it as a teammate tab (crewTeamHomeAgentTab).
	WatchKey string `json:"watch_key,omitempty"`
	crewTeamMessagePreview
}

// crewTeamHomeMain is the Team's online home and its Main (the Team agent) when the viewer is admitted to it.
func crewTeamHomeMain(t crewTeam, viewer string) (crewTeam, string, crewTeamRegisteredAgent, bool) {
	if t.OnlineHome == "" || t.RosterSource != "" || !crewTeamAccountActive(viewer) {
		return crewTeam{}, "", crewTeamRegisteredAgent{}, false
	}
	home, err := readCrewTeam(t.OnlineHome)
	if err != nil || home.RosterSource != t.ProjectID || !crewTeamAccountActive(home.OwnerID) {
		return crewTeam{}, "", crewTeamRegisteredAgent{}, false
	}
	key := crewOwnerMainChatKey(t.OnlineHome)
	registered, ok := home.registeredAgent(home.OwnerID, key)
	if key == "" || !ok || !crewTeamOnlineCopyAdmits(t, viewer, registered) {
		return crewTeam{}, "", crewTeamRegisteredAgent{}, false
	}
	return home, key, registered, true
}

// crewTeamHomeAgentTab is the Team agent as one more teammate tab of the Team's Crew (owner, 15 Nov: "clicking Team
// agent should open normally, like another user's agents"): read only, its chat read from the home, asked through
// AgentBridge like any teammate's agent. Never a route into the home Crew.
func crewTeamHomeAgentTab(ctx context.Context, project, viewer string) (AgentProfileSideChat, bool) {
	t, err := readCrewTeam(project)
	if err != nil {
		return AgentProfileSideChat{}, false
	}
	home, key, registered, ok := crewTeamHomeMain(t, viewer)
	if !ok {
		return AgentProfileSideChat{}, false
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(home.OwnerID))
	if err != nil {
		return AgentProfileSideChat{}, false
	}
	for _, record := range doc.Entries {
		if record.ConversationKey == key && strings.TrimSpace(record.SessionID) != "" {
			return AgentProfileSideChat{ConversationKey: key, ConversationID: record.ConversationID, SessionID: record.SessionID,
				Name: firstNonEmptyTrimmed(registered.Name, "Team agent"), Pinned: true, TabOwnerID: home.OwnerID, TabOwnerName: "Team",
				Purpose: "Always online · set up Team automations and how the team works"}, true
		}
	}
	return AgentProfileSideChat{}, false
}

func crewTeamHomeAgentFor(ctx context.Context, t crewTeam, viewer string) *crewTeamHomeAgent {
	// Decoration only: callers pass rows already admitted by crewTeamOnlineCopyAdmits (owner always, even Team off).
	home, key, registered, ok := crewTeamHomeMain(t, viewer)
	if !ok {
		return nil
	}
	out := &crewTeamHomeAgent{WatchKey: key, Name: firstNonEmptyTrimmed(registered.Name, "Team agent"), Line: "Always online · set up Team automations and how the team works", Address: crewTeamAgentAddress(t.OnlineHome, registered.ID)}
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
	WatchKey string `json:"watch_key,omitempty"`
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
				projected.WatchKey = home.WatchKey
			}
		} else if agent, ok := byID[row.ID]; ok {
			projected.crewTeamMessagePreview = preview(row.PersonID, agent)
		}
		out = append(out, projected)
	}
	return out
}
