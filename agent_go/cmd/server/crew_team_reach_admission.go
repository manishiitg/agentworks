package server

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// A Team row is the grant. No cached/persisted duplicate audience is consulted.
// Every bridge operation resolves the same current rows used by discovery.
func crewTeamAdmitAddress(ctx context.Context, viewer, address string) (crewTeamReachableAgent, crewTeam, error) {
	address = strings.TrimSpace(address)
	var candidates []crewTeamReachableAgent
	if rest, ok := strings.CutPrefix(address, "workspace:"+crewProfileID+":"); ok {
		project, _, valid := strings.Cut(rest, "/agent/")
		if valid {
			if t, err := readCrewTeam(project); err == nil {
				candidates = crewTeamReachableIn(ctx, t, viewer)
			}
		}
	} else if strings.HasPrefix(address, "agent:") {
		candidates = crewTeamReachableAgents(ctx, viewer)
	}
	for _, row := range candidates {
		if row.Address != address {
			continue
		}
		t, err := readCrewTeam(row.Team)
		if err != nil {
			break
		}
		// Re-read after discovery: no stale membership, rule or transport snapshot.
		for _, current := range crewTeamReachableIn(ctx, t, viewer) {
			if current.ID == row.ID && current.Address == address {
				return current, t, nil
			}
		}
	}
	log.Printf("[TEAM_AGENT_REACH] caller=%s event=admit outcome=refused reason=not_reachable", viewer)
	return crewTeamReachableAgent{}, crewTeam{}, admissionRefused("This agent is not In team for you now. Refresh the agent list.")
}

// Routing is not a second grant: the original target was admitted above and
// the Team agent is selected from that same viewer's current reachable rows.
func crewTeamRouteAsk(ctx context.Context, t crewTeam, row crewTeamReachableAgent) (crewTeamReachableAgent, error) {
	if row.Online {
		return row, nil
	}
	viewer := GetUserIDFromContext(ctx)
	for _, candidate := range crewTeamReachableIn(ctx, t, viewer) {
		if candidate.Kind == "team_agent" {
			return candidate, nil
		}
	}
	log.Printf("[TEAM_AGENT_REACH] project=%s agent=%s caller=%s event=ask outcome=refused reason=offline_no_team_agent", t.ProjectID, row.ID, viewer)
	return crewTeamReachableAgent{}, fmt.Errorf("This agent is offline. Try later, or set up the Team agent in AgentWorks.")
}
