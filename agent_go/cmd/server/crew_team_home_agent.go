package server

import "context"

// crewTeamHomeAgent is the Team agent (the Team's online home Main, PLAT-746-1022) as the right panel's Agents list
// shows it to a current member: always online, answering when the others are away. Its last line comes from the home
// Main chat, which every current member may read (they are all Builders of the Team Crew).
type crewTeamHomeAgent struct {
	Name    string `json:"name"`
	Line    string `json:"line"`
	Address string `json:"address,omitempty"`
	crewTeamMessagePreview
}

func crewTeamHomeAgentFor(ctx context.Context, t crewTeam, viewer string) *crewTeamHomeAgent {
	if !t.On || t.OnlineHome == "" || t.RosterSource != "" || !t.member(viewer) || !crewTeamAccountActive(viewer) {
		return nil
	}
	out := &crewTeamHomeAgent{Name: "Team agent", Line: "Online always · answers when others are away"}
	owner, key := "team-"+t.ProjectID, crewOwnerMainChatKey(t.OnlineHome)
	if home, err := readCrewTeam(t.OnlineHome); err == nil {
		if a, ok := home.registeredAgent(owner, key); ok && a.InTeam {
			out.Address = crewTeamAgentAddress(t.OnlineHome, a.ID)
		}
	}
	out.crewTeamMessagePreview = crewTeamMessagePreviews(ctx, owner, t.OnlineHome)[key]
	out.LastMessage = crewTeamMessageLine(out.LastMessage)
	if out.LastMessage == "" {
		out.LastMessageAt = ""
	}
	return out
}
