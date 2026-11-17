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
	people := []crewTeamAgentsPerson{{UserID: "owner", Name: "Owner"}, {UserID: "member", Name: "Member", Active: true}}
	for _, a := range team.Agents {
		i := 0
		if a.OwnerID == "member" {
			i = 1
		}
		people[i].Agents = append(people[i].Agents, crewTeamAgent{ID: a.ID, Tab: a.Tab, ChatKey: a.ChatKey, Name: a.Name, Purpose: "Review shared changes", Examples: []string{"Check a patch"}, InTeam: a.InTeam, Activity: "running"})
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
	peer := crewTeamLiveFor(t.Context(), team, people, "owner")
	raw, _ := json.Marshal(peer)
	if strings.Contains(string(raw), device) || strings.Contains(string(raw), "Example Mac") {
		t.Fatalf("another person's device leaked: %s", raw)
	}
	for _, a := range peer.Agents {
		if a.Purpose != "Review shared changes" || len(a.Examples) != 1 || a.Examples[0] != "Check a patch" {
			t.Fatal("admitted monitor row lost purpose metadata")
		}
		if a.OwnerID == "member" && a.Tab == "private" {
			t.Fatal("private agent leaked through monitor")
		}
	}
}
