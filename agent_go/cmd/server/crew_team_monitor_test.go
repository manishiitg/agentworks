package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
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
	if got := crewTeamMonitorWorkFor(peer, lastWork.Add(2*time.Hour)); got.LastWorkedLabel != "last worked 2h ago" {
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
	if work.Working != 3 || len(work.Lines) != 2 || work.More != "+1 more" || !strings.Contains(work.Lines[0], "Colleague · a · Fixing monitor rows") {
		t.Fatalf("not a compact Team summary: %+v", work)
	}
	live.Agents = nil
	quiet := crewTeamMonitorWorkFor(live, now)
	if quiet.Label != "" || quiet.LastWorkedLabel != "last worked 2h ago" {
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
}
