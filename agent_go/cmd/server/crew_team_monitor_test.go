package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
)

// #2917: self devices are the viewer's metadata only; another member never gets them.
func TestCrewTeamMonitorSelfDeviceStaysOwnerOnly(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	newExternalCrewCommandFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"editor","products":["work"]},{"id":"member","username":"member","role":"editor","products":["work"]}]}`)
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "member", Where: "mac", MacPair: "pair"}}, Agents: crewTeamAgentFixtures("team", map[string]map[string]bool{"owner": {"main": true}, "member": {"helper": true, "private": false}})}
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	team, _ = readCrewTeam("team")
	if !crewTeamReachOpen(team, "owner") || !crewTeamReachOpen(team, "member") {
		t.Fatal("fixture must admit both current people")
	}
	device := desktopRelayDeviceID("saved")
	sock := &crewMacSocket{owner: "member", device: device, name: "Example Mac", twins: map[string]string{"team": "pair"}, lastSeen: time.Now()}
	key := "member/" + device
	crewMacSockets.Lock()
	previous := crewMacSockets.byDevice[key]
	crewMacSockets.byDevice[key] = sock
	crewMacSockets.Unlock()
	t.Cleanup(func() {
		crewMacSockets.Lock()
		delete(crewMacSockets.byDevice, key)
		if previous != nil {
			crewMacSockets.byDevice[key] = previous
		}
		crewMacSockets.Unlock()
	})
	lastWork := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	people := []crewTeamAgentsPerson{{UserID: "owner", Name: "Owner"}, {UserID: "member", Name: "Member", Active: true}}
	for _, a := range team.Agents {
		i := 0
		if a.OwnerID == "member" {
			i = 1
		}
		at := lastWork
		if a.Tab == "private" {
			at = lastWork.Add(24 * time.Hour)
		}
		people[i].Agents = append(people[i].Agents, crewTeamAgent{lastWorkedAt: at, ID: a.ID, Tab: a.Tab, ChatKey: a.ChatKey, Name: a.Name, Purpose: "Review shared changes", Examples: []string{"Check a patch"}, InTeam: a.InTeam, Activity: "running"})
	}
	own := crewTeamLiveFor(t.Context(), team, people, "member")
	if len(own.Agents) < 2 {
		t.Fatalf("owner must see own joined/private agents: %+v", own)
	}
	for _, a := range own.Agents {
		if a.OwnerID == "member" && (!a.You || a.Device != device) {
			t.Fatalf("own exact device missing: %+v", a)
		}
	}
	crewTeamLiveLocalSelf(&own, desktopConnection{ID: "saved", User: UserInfo{ID: "member"}})
	for _, p := range own.People {
		if p.UserID == "member" && p.Name != "You (this Mac)" {
			t.Fatalf("exact local relay not recognised: %+v", p)
		}
	}
	for i := range people {
		for j := range people[i].Agents {
			people[i].Agents[j].Activity = ""
		}
	}
	peer := crewTeamLiveFor(t.Context(), team, people, "owner")
	if got := crewTeamMonitorWorkFor(peer, lastWork.Add(2*time.Hour)); got.LastWorkedLabel != "2h ago" {
		t.Fatalf("private agent recency leaked: %+v", got)
	}
	raw, _ := json.Marshal(peer)
	if strings.Contains(string(raw), device) || strings.Contains(string(raw), "Example Mac") {
		t.Fatalf("another person's device leaked: %s", raw)
	}
	for _, a := range append(peer.Agents, peer.Idle...) {
		if a.Purpose != "Review shared changes" || len(a.Examples) != 1 || a.Examples[0] != "Check a patch" {
			t.Fatal("admitted monitor row lost purpose metadata")
		}
		if a.OwnerID == "member" && a.Tab == "private" {
			t.Fatal("private agent leaked through monitor")
		}
	}
}

// #3005: summaries are bounded Teams, not a second agent/transcript reader.
func TestCrewTeamMonitorWorkSummaryIsBoundedAndQuietRecencyIsReal(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	live := crewTeamLiveView{lastWorkedAt: now.Add(-2 * time.Hour)}
	for _, id := range []string{"c", "a", "b"} {
		live.Agents = append(live.Agents, crewTeamLiveAgent{ID: id, OwnerID: "member", Person: "Colleague", Name: id, Activity: "running", crewTeamMessagePreview: crewTeamMessagePreview{LastMessage: "Fixing monitor rows"}})
	}
	work := crewTeamMonitorWorkFor(live, now)
	if work.LastWorkedAt != live.lastWorkedAt.Format(time.RFC3339Nano) {
		t.Fatal("snapshot time replaced real activity and would shuffle active Teams")
	}
	// Owner, 27 Nov: the Team menu counts people, not agents; one person with three agents is one person working.
	if work.Working != 1 || work.Label != "1 person working" {
		t.Fatalf("not a compact Team summary: %+v", work)
	}
	live.Agents = nil
	quiet := crewTeamMonitorWorkFor(live, now)
	if quiet.Label != "" || quiet.LastWorkedLabel != "2h ago" {
		t.Fatalf("quiet Team invented work: %+v", quiet)
	}
	unknown := crewTeamMonitorWorkFor(crewTeamLiveView{}, now)
	if unknown.LastWorkedAt != "" || unknown.LastWorkedLabel != "" {
		t.Fatal("unknown activity invented recency")
	}
	// An idle relay heartbeat is not recent work; only its actual last transition is.
	key := "monitor-test/team/chat"
	crewRelayStreams.Lock()
	crewRelayStreams.streams[key] = &crewRelayStream{since: now.Add(-2 * time.Hour), activityAt: now}
	crewRelayStreams.Unlock()
	t.Cleanup(func() { crewRelayStreams.Lock(); delete(crewRelayStreams.streams, key); crewRelayStreams.Unlock() })
	if got := crewMacConversationLastWorkedAt("monitor-test", "team", "chat"); !got.Equal(now.Add(-2 * time.Hour)) {
		t.Fatal("idle heartbeat counted as work")
	}
	// The same for a server chat: the idle sweep marks it inactive and moves
	// LastActivity to now; only its real work time may count.
	old := runningServerAPI
	runningServerAPI = &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{"server-chat": {LastActivity: now, LastWorkedAt: now.Add(-2 * time.Hour)}}}
	t.Cleanup(func() { runningServerAPI = old })
	if got := crewServerChatLastWorkedAt("server-chat"); !got.Equal(now.Add(-2 * time.Hour)) {
		t.Fatal("idle sweep counted as work")
	}
}

// #3900: Mac-run work is recorded on the Mac. A commit seen both here and from a Mac (or from two worktrees) counts
// once, and a Mac agent's tokens add to the same person's tokens recorded here.
func TestCrewTeamMonitorMacUsageCountsOnce(t *testing.T) {
	now := time.Now()
	here := []crewTeamOverviewCommit{{Hash: "a", Title: "Fix menu", At: now.Add(-time.Hour)}}
	mac := []crewTeamOverviewCommit{{Hash: "a", Title: "Fix menu", At: now.Add(-time.Hour)}, {Hash: "b", Title: "Add tokens", At: now.Add(-2 * time.Hour)}, {Hash: "c", Title: "Old", At: now.Add(-48 * time.Hour)}}
	if got := crewTeamOverviewMergeCommits(here, mac, now.Add(-24*time.Hour)); len(got) != 2 || got[1].Hash != "b" {
		t.Fatalf("commits = %+v, want a and b once", got)
	}
	name := func(user string) string { return user }
	out := crewTeamOverviewAddMacTokens([]crewTeamOverviewTokens{{Person: "owner", Tokens: 100, userID: "owner", Agents: []crewTeamOverviewAgentTokens{{Agent: "Main", Tokens: 100}}}},
		map[string]map[string]int64{"owner": {"Main": 50, "Coder": 500}, "teammate": {"Main": 10}}, name)
	if len(out) != 2 || out[0].Tokens != 650 || out[0].Agents[0].Agent != "Coder" || out[0].Agents[1].Tokens != 150 || out[1].Tokens != 10 {
		t.Fatalf("tokens = %+v", out)
	}
}

// A Team showed 1,311M tokens for one day: cached prompt tokens, re-read on every turn, were counted as work (#3900).
func TestCrewTeamMonitorTokensLeaveOutCachedPrompt(t *testing.T) {
	var a costledger.Aggregate
	a.AddEntry(costledger.Entry{Provider: "claude-code", PromptTokens: 1_000, CacheReadTokens: 90_000, CacheWriteTokens: 2_000, CompletionTokens: 500})
	if got := crewTeamWorkTokens(a); got != 3_500 {
		t.Fatalf("work tokens = %d, want new input (prompt + cache write) and output only", got)
	}
}
