package server

import (
	"context"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-856-523: selecting one agent cannot move another tab's callers, and
// merging its callers must retain destination schedules that already exist.
func TestPublishAgentKeepsOtherAgentsCallers(t *testing.T) {
	h := crewMoveHeader{SourceID: "crew", MainKey: "crew", AgentTab: "writer", Schedules: []projectactions.Schedule{
		{ID: "main", Action: projectactions.Action{Type: "message"}},
		{ID: "selected", Action: projectactions.Action{Type: "message", Tab: "crew:chat:writer"}},
		{ID: "other", Action: projectactions.Action{Type: "message", Tab: "crew:chat:reader"}},
	}, Watchers: map[string]crewMoveWatcherState{"main": {}, "selected": {}, "other": {}}}
	if err := filterCrewAgentCallers(&h); err != nil {
		t.Fatal(err)
	}
	if len(h.Schedules) != 1 || h.Schedules[0].ID != "selected" || len(h.Watchers) != 1 {
		t.Fatalf("moved another agent's callers: %+v", h)
	}
	old := []projectactions.Schedule{{ID: "already-online", Enabled: true}, {ID: "selected"}}
	h.Schedules[0].Enabled = true
	merged := mergeCrewPublishedItems(old, h.Schedules, func(s projectactions.Schedule) string { return s.ID })
	if len(merged) != 2 || merged[0].ID != "already-online" || !merged[0].Enabled || !merged[1].Enabled {
		t.Fatalf("lost or duplicated a destination schedule: %+v", merged)
	}
}

func TestPublishAgentFileSelectionCannotEscapeCrew(t *testing.T) {
	for _, p := range []string{"../private", "/etc", "src/../private", "a\\b", "a\x00b"} {
		if _, err := cleanCrewAgentPublishSelection(crewAgentPublishSelection{Tab: "writer", Paths: []string{p}}); err == nil {
			t.Fatalf("accepted %q", p)
		}
	}
	selection, err := cleanCrewAgentPublishSelection(crewAgentPublishSelection{Tab: "writer", Paths: []string{"src/app"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		key             string
		directory, want bool
	}{
		{"files/src", true, true}, {"files/src/app/main.go", false, true},
		{"files/src/other.go", false, false}, {"resources/skills/writer/SKILL.md", false, true},
		{"resources/MEMORY.md", false, true}, {"knowledge/knowledgebase/private.md", false, false},
		{"resources/published-agents/other/MEMORY.md", false, false},
	} {
		if got := crewAgentPublishIncludes(selection, test.key, test.directory); got != test.want {
			t.Errorf("%s included=%t want=%t", test.key, got, test.want)
		}
	}
}

func TestPublishAgentRefusesArchiveInsteadOfClaimingNativeResume(t *testing.T) {
	api := &StreamingAPI{}
	err := api.importCrewHandoffConversation(context.Background(), &UserClaims{UserID: "owner"}, crewroot.Roots{}, "pair", "transfer", &crewHandoffConversation{ConversationID: "conversation-test"}, ProductConversationRecord{}, true)
	if err == nil {
		t.Fatal("native publish accepted an archive without its native session")
	}
}
