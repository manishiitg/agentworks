package server

import "testing"

// Rule (PLAT-746, tab setup icon): an automation is listed only on the agent it starts, and only on the Crew owner's
// agents (schedules run as the owner); a teammate's agent with the same tab id never shows the owner's automations.
func TestCrewTabSetupListsAutomationsOnlyOnTheirAgent(t *testing.T) {
	team := crewTeam{ProjectID: "crew-1", OwnerID: "owner", On: true}
	src := crewTabSetupSource{automations: []crewTabAutomation{
		{Tab: macMainTab, Kind: "Webhook", Name: "Order created", On: true},
		{Tab: "abc", Kind: "Schedule", Name: "Daily report", On: false},
	}}
	cases := []struct {
		name, person, tab string
		inTeam            bool
		want              []string
		reach             string
		starts            bool
	}{
		{"owner main gets main's", "owner", macMainTab, true, []string{"Order created"}, "team", true},
		{"owner tab gets its own, off adds no bolt", "owner", "abc", false, []string{"Daily report"}, "", false},
		{"owner other tab gets none", "owner", "xyz", true, nil, "team", false},
		{"teammate's same tab id gets none", "member", "abc", true, nil, "team", false},
	}
	for _, c := range cases {
		got := src.setup(team, crewTeamAgent{Tab: c.tab, ChatKey: "crew-1" + codeChatSideMarker + c.tab}, c.person, c.inTeam, nil)
		var names []string
		for _, item := range got.Automations {
			names = append(names, item.Name)
		}
		if len(names) != len(c.want) || (len(names) > 0 && names[0] != c.want[0]) || got.Reach != c.reach || got.StartsOnOwn != c.starts {
			t.Errorf("%s: got %v reach=%q starts=%v", c.name, names, got.Reach, got.StartsOnOwn)
		}
	}
}
