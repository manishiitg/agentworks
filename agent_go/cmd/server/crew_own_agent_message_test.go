package server

import "testing"

// PLAT-856-564 (owner, 12 Nov): an agent and its online copy talk through Team messaging. Your own agent of the same
// Crew reaches your other In team agent; an Only me agent, another Crew's agent and a Run person are still refused.
func TestCrewOwnTeamAgentMessageOnlySameCrewInTeam(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	newExternalCrewCommandFixture(t)
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true}
	team.Agents = crewTeamAgentFixtures("team", map[string]map[string]bool{"owner": {"helper01": true, "private1": false}})
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatalf("team fixture: %v", err)
	}
	dest := agentMessageEndpoint{UserID: "owner", ID: "team"}
	own := agentMessageEndpoint{UserID: "owner", ID: "team", Kind: "crew"}
	if !crewOwnTeamAgentMessage("team", "helper01", own, dest) {
		t.Fatal("your own agent of this Crew must reach your other In team agent")
	}
	if crewOwnTeamAgentMessage("team", "private1", own, dest) {
		t.Fatal("an Only me agent must stay private, even to your own agents")
	}
	if crewOwnTeamAgentMessage("team", "helper01", agentMessageEndpoint{UserID: "owner", ID: "other-crew"}, dest) {
		t.Fatal("your agent in another Crew is not a Team message")
	}
	if crewOwnTeamAgentMessage("team", "helper01", agentMessageEndpoint{UserID: "owner", ID: "team", Mode: "run"}, dest) {
		t.Fatal("a Run mode chat is a Run person using the Crew, not your own agent")
	}
	if crewOwnTeamAgentMessage("team", "helper01", agentMessageEndpoint{UserID: "member", ID: "team"}, dest) {
		t.Fatal("a teammate's agent keeps the Team audience check")
	}
}
