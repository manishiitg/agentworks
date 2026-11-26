package server

import (
	"strings"
	"testing"
)

// PLAT-746-904: a quiet Team is one header, not a repeated people/agent/sync inventory. Your own working agents are
// not Team news (the left menu shows them), so they never fill the card.
func TestCrewTeamCardShowsOnlyLiveSharedAgentsAndProblems(t *testing.T) {
	team := crewTeam{On: true, Folder: crewTeamFolder{Folders: []string{"team"}}}
	facts := crewAccessFacts{ServerCrew: true, TeamKnown: true, Team: &team, Viewer: "owner", People: []crewTeamAgentsPerson{
		{UserID: "owner", You: true, Name: "Example Owner", Agents: []crewTeamAgent{{Name: "Private agent", Activity: "running", InTeam: false}, {Name: "Own shared", Activity: "running", InTeam: true}}},
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
		if strings.Contains(line.Title, "Private agent") || strings.Contains(line.Title, "Own shared") {
			t.Fatal("Only me activity or your own agent's work filled the Team card")
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
	// Retained people labels after a failed Mac read must not claim live work.
	facts.SuppressLive = true
	for _, line := range crewAccessTree(facts) {
		if line.Kind == "team_live" {
			t.Fatal("last-good people must not produce live activity after a failed read")
		}
	}

}

// PLAT-746-904: absent a saved first name, login punctuation/digits are not a person's name.
func TestCrewTeamFirstNameFromLogin(t *testing.T) {
	for input, want := range map[string]string{"example.member42@example.com": "Example", "ravi_reports": "Ravi", "asha123": "Asha", "Asha Patel": "Asha", "Élodie Martin": "Élodie", "": "Teammate"} {
		if got := crewTeamFirstName(input); got != want {
			t.Errorf("%q: got %q want %q", input, got, want)
		}
	}
}

// A short label must not turn two different people into one on screen.
func TestCrewTeamDistinctNamesInOneTeam(t *testing.T) {
	raw := map[string]string{"a": "Sam Khan", "b": "Sam Roy", "c": "ana1@example.com", "d": "ana2@example.com", "e": "Élodie Martin"}
	labels := crewTeamDistinctNames(raw)
	for id, want := range map[string]string{"a": "Sam K.", "b": "Sam R.", "c": "ana1", "d": "ana2", "e": "Élodie"} {
		if labels[id] != want {
			t.Errorf("%s got %q want %q", id, labels[id], want)
		}
	}
}
