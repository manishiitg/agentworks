package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// The Access row's summary after where the Crew lives (owner, 5 Nov: "we should show team also"), written once here
// for the Mac app and the website alike: "Team: 2 people · 3 agents in team · 1 public on AgentBridge", or
// "Team off". Parts that are zero are left out, and the Team part when it could not be read just now (unknown).
func crewAccessSummary(team *crewTeam, agents []macSharedAgent, unknown ...bool) string {
	parts := []string{}
	if len(unknown) > 0 && unknown[0] {
		// say nothing about the Team rather than something wrong
	} else if team == nil || !team.On {
		parts = append(parts, "Team off")
	} else {
		people := 0
		for _, p := range team.People {
			if !p.Invited && p.UserID != team.OwnerID {
				people++
			}
		}
		if people > 0 {
			parts = append(parts, fmt.Sprintf("Team: %d %s", people, plural(people, "person", "people")))
		} else {
			parts = append(parts, "Team on")
		}
		if n := countAgents(agents, crewTeamInTeam); n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s in team", n, plural(n, "agent", "agents")))
		}
	}
	if n := countAgents(agents, func(a macSharedAgent) bool { return !a.Off && a.AgentBridgePublic }); n > 0 {
		parts = append(parts, fmt.Sprintf("%d public on AgentBridge", n))
	}
	return strings.Join(parts, " · ")
}

// crewMemberAccessSummary is the short Access line a Team member sees: "Team · 3 people" (owner, 5 Nov: the longer
// "Team · demo's Crew · 3 people" was cut off in the side panel).
func crewMemberAccessSummary(t crewTeam) string {
	people := 1 // the owner
	for _, p := range t.People {
		if !p.Invited && p.UserID != t.OwnerID {
			people++
		}
	}
	return fmt.Sprintf("Team · %d %s", people, plural(people, "person", "people"))
}

func countAgents(agents []macSharedAgent, keep func(macSharedAgent) bool) int {
	n := 0
	for _, agent := range agents {
		if keep(agent) {
			n++
		}
	}
	return n
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// crewAccessSummaryFor builds the summary for this Crew where this server stands: online the Team is read directly;
// on the Mac it comes from the same cached read of the online copy as the Access tree (crewAccessRemoteRead).
func (api *StreamingAPI) crewAccessSummaryFor(ctx context.Context, owner, project string, b *crewroot.SyncBinding) string {
	agents := readMacSharedAgents(project)
	if !IsLocalMode() {
		if team, err := readCrewTeam(project); err == nil {
			return crewAccessSummary(&team, agents)
		}
		return crewAccessSummary(nil, agents)
	}
	if b == nil || b.Side != "local" || b.PeerProjectID == "" {
		return crewAccessSummary(nil, agents)
	}
	c, err := desktopConnectionFor(owner, b.ConnectionID)
	if err != nil {
		return crewAccessSummary(nil, agents, true)
	}
	remote := api.crewAccessRemoteRead(ctx, owner, c, project, b.PeerProjectID)
	if remote.team == nil {
		return crewAccessSummary(nil, agents, true)
	}
	return crewAccessSummary(remote.team, agents)
}
