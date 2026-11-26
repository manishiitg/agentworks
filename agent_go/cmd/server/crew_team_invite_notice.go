package server

import "fmt"

// Presentation only. Invitation admission and creation still use the ordinary join handlers.
type crewTeamInviteNotice struct {
	Line               string `json:"line"`
	Multiple           string `json:"multiple"`
	Joined             string `json:"joined"`
	Join               string `json:"join"`
	NotNow             string `json:"not_now"`
	JoinAll            string `json:"join_all"`
	Review             string `json:"review"`
	Open               string `json:"open"`
	ConnectionRequired string `json:"connection_required"`
}

func crewTeamInvitePresentation(t crewTeam) crewTeamInviteNotice {
	people, agents := 1, 0
	for _, p := range t.People {
		if !p.Invited && crewTeamAccountActive(p.UserID) {
			people++
		}
	}
	for _, a := range t.Agents {
		if a.InTeam && t.member(a.OwnerID) && crewTeamAccountActive(a.OwnerID) {
			agents++
		}
	}
	return crewTeamInviteNotice{
		Line:     fmt.Sprintf("%s invited you to %s · %d people · %d agents", shareOwnerLabel(t.OwnerID), t.Name, people, agents),
		Multiple: "{count} Team invites · {names}", Joined: crewTeamJoinedNotice(t.Name, ""),
		Join: "Join", NotNow: "Not now", JoinAll: "Join all", Review: "Review", Open: "Open",
		ConnectionRequired: "Connect this Team's server in AgentWorks, then try Join again.",
	}
}
func crewTeamJoinedNotice(name, folder string) string {
	if folder == "" {
		return fmt.Sprintf("Joined %s · its own project · available online", name)
	}
	return fmt.Sprintf("Joined %s · its own project · files syncing to %s", name, folder)
}
