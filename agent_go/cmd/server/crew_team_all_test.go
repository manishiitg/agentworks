package server

import "testing"

// PLAT-746-904: a saved deleted tab blocked the whole Mac roster push,
// leaving new agents absent from the Team registry.
func TestCrewTeamRosterDropsMissingTabsAndAddsNewAgents(t *testing.T) {
	rows := []macAgentRow{{Tab: "main"}, {Tab: "new"}}
	next, changed := crewTeamRoster(rows, []macSharedAgent{{Tab: "main", Audience: "team"}, {Tab: "deleted", Audience: "team"}}, true)
	if !changed || len(next) != 2 || next[0].Tab != "main" || next[1].Tab != "new" || next[1].Audience != "team" {
		t.Fatalf("roster did not replace missing tab: %+v", next)
	}
}
