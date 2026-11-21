package server

import (
	"strings"
	"testing"
)

// Registry name lookup never reveals private rows or expands Team admission.
func TestCrewTeamMessageNamesStayInsideAdmittedRegistry(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	newExternalCrewCommandFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"alex.owner","role":"editor","products":["work"]},{"id":"other","username":"blair.member","role":"editor","products":["work"]}]}`)
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}}, Agents: crewTeamAgentFixtures("team", map[string]map[string]bool{"owner": {"helper": true}, "other": {"helper": true, "private": false}})}
	for id, a := range team.Agents {
		a.Name = a.Tab
		team.Agents[id] = a
	}
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	people := teamAgentsByPerson(t.Context(), team, "owner")
	var chosen crewTeamAgent
	var person string
	for _, p := range people {
		for _, a := range p.Agents {
			if p.UserID == "other" && a.InTeam {
				chosen = a
				person = crewTeamPersonLabels(team)[p.UserID]
			}
		}
	}
	if chosen.ID == "" {
		t.Fatal("missing allowed target fixture")
	}
	got, owner, err := crewTeamMessageAgent(t.Context(), "team", "owner", person+"'s "+chosen.Name)
	if err != nil || got.ID != chosen.ID || owner != "other" {
		t.Fatalf("qualified name: %s %s %v", got.ID, owner, err)
	}
	if _, _, err := crewTeamMessageAgent(t.Context(), "team", "owner", chosen.Name); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("ambiguous name: %v", err)
	}
	typo := chosen.ID + "x"
	if _, _, err := crewTeamMessageAgent(t.Context(), "team", "owner", typo); err == nil || !strings.Contains(err.Error(), chosen.ID) || strings.Contains(err.Error(), "private") {
		t.Fatalf("closest admitted choices: %v", err)
	}
	if _, _, err := crewTeamMessageAgent(t.Context(), "team", "outsider", chosen.Name); err == nil || strings.Contains(err.Error(), chosen.ID) {
		t.Fatalf("outsider discovery: %v", err)
	}
}
