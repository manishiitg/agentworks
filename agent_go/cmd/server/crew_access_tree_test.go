package server

import (
	"strings"
	"testing"
)

// PLAT-746-904: a quiet Team is one header, not a repeated people/agent/sync inventory.
func TestCrewTeamCardShowsOnlyLiveSharedAgentsAndProblems(t *testing.T) {
	team := crewTeam{On: true, Folder: crewTeamFolder{Whole: true}}
	facts := crewAccessFacts{ServerCrew: true, TeamKnown: true, Team: &team, Viewer: "owner", People: []crewTeamAgentsPerson{
		{UserID: "owner", You: true, Name: "Example Owner", Agents: []crewTeamAgent{{Name: "Private agent", Activity: "running", InTeam: false}}},
		{UserID: "member", Name: "Example Member", Agents: []crewTeamAgent{{Name: "Research", InTeam: true}}},
	}}
	lines := crewAccessTree(facts)
	if len(lines) != 1 || lines[0].Kind != "team" || strings.Contains(lines[0].Title, "other") {
		t.Fatalf("quiet Team must have only its named header: %+v", lines)
	}
	for i := 0; i < 5; i++ {
		facts.People[1].Agents = append(facts.People[1].Agents, crewTeamAgent{Name: "Working", InTeam: true, Activity: "running"})
	}
	facts.LocalError = "Connection stopped"
	facts.HasLocal = true
	facts.Clashes = []crewTeamClash{{Path: "team/example.txt"}, {Path: "team/resolved.txt", Resolved: true}}
	lines = crewAccessTree(facts)
	live, problems := 0, 0
	for _, line := range lines {
		if strings.Contains(line.Title, "Private agent") {
			t.Fatal("Only me activity leaked into Team card")
		}
		if line.Kind == "team_live" {
			live++
			if line.Target != "activity" {
				t.Fatal("live row must open Activity")
			}
		}
		if line.Kind == "clash" || line.Kind == "folder" {
			problems++
		}
	}
	if live != 3 || problems != 2 {
		t.Fatalf("want three live rows and just open problems: %+v", lines)
	}
}
