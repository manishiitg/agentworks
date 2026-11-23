package server

import (
	"testing"
	"time"
)

// PLAT-746-979: an idle teammate's agent appears nowhere (left menu is own-only, the header panel and Team Agents
// listed only working/waiting). Team › Agents keeps one Teammates section grouped by person where every In-team
// agent carries one status line: working shows its Doing post, waiting shows who it waits on, idle shows its last
// activity time. Only me agents stay hidden.
func TestCrewShareTeammateStatusLinesShowIdle(t *testing.T) {
	twoHoursAgo := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	rows := []crewTeamTabAgent{
		{Key: "working-id", WatchKey: "working-chat", Name: "Helper", InTeam: true, OwnerID: "teammate", OwnerName: "Teammate", Activity: "running"},
		{Key: "waiting-id", WatchKey: "waiting-chat", Name: "Research", InTeam: true, OwnerID: "teammate", OwnerName: "Teammate", Activity: "waiting", StatusLabel: "Waiting for Teammate's reply"},
		{Key: "idle-id", WatchKey: "idle-chat", Name: "Docs", InTeam: true, OwnerID: "teammate", OwnerName: "Teammate"},
		{Key: "mac-off-id", WatchKey: "mac-off-chat", Name: "MacHelper", InTeam: true, OwnerID: "teammate", OwnerName: "Teammate", StatusLabel: "Mac off"},
		{Key: "quiet-id", WatchKey: "quiet-chat", Name: "Quiet", InTeam: true, OwnerID: "teammate", OwnerName: "Teammate"},
		{Key: "mine-id", WatchKey: "mine-chat", Name: "Mine", InTeam: true, Mine: true, OwnerID: "viewer", OwnerName: "Viewer"},
		{Key: "private-id", WatchKey: "private-chat", Name: "Secret", OwnerID: "teammate", OwnerName: "Teammate"},
	}
	live := crewTeamLiveView{
		Agents: []crewTeamLiveAgent{
			{ID: "working-id", ChatKey: "working-chat", Doing: "Editing the login page", Activity: "running"},
			{ID: "waiting-id", ChatKey: "waiting-chat", Waiting: "Waiting for Teammate's reply", Activity: "waiting"},
		},
		Idle: []crewTeamLiveAgent{
			{ID: "idle-id", ChatKey: "idle-chat", crewTeamMessagePreview: crewTeamMessagePreview{LastMessageAt: twoHoursAgo}},
		},
	}
	team := crewTeam{OwnerID: "owner"}
	out := crewShareSharedWithYou(team, "viewer", rows, live, nil)
	if len(out) != 1 {
		t.Fatalf("expected one teammate group, got %+v", out)
	}
	lines := map[string]string{}
	for _, item := range out[0].Items {
		lines[item.Name] = item.Line
		// Real bug (2026-11-17): rows sent the Team agent ID, so clicking a
		// teammate's agent never found its chat. A row opens the chat key.
		if item.Name == "Helper" && (item.Key != "working-chat" || item.OwnerID != "teammate") {
			t.Fatalf("teammate row must open its chat key for its owner: %+v", item)
		}
	}
	if len(lines) != 5 {
		t.Fatalf("expected five shared agents including idle, got %+v", lines)
	}
	if lines["Helper"] != "Editing the login page" {
		t.Fatalf("working agent lost its Doing post: %q", lines["Helper"])
	}
	if lines["Research"] != "Waiting for Teammate's reply" {
		t.Fatalf("waiting agent lost who it waits on: %q", lines["Research"])
	}
	if lines["Docs"] != "Last active 2 h ago" {
		t.Fatalf("idle agent lost its last activity time: %q", lines["Docs"])
	}
	if lines["MacHelper"] != "Mac off" {
		t.Fatalf("idle Mac-off agent lost its status: %q", lines["MacHelper"])
	}
	if lines["Quiet"] != "Idle" {
		t.Fatalf("idle agent with no activity should say Idle: %q", lines["Quiet"])
	}
}
