package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-746-951: the Mac reads the server inbox using the same paired Crew
// endpoint that sent it, not a named-agent registration or an inbox prefix.
func TestCrewTeamPairedReadUsesStoredSenderIdentity(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	newExternalCrewCommandFixture(t)
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}}, Agents: crewTeamAgentFixtures("team", map[string]map[string]bool{"owner": {"sender": true, "sibling": true}, "other": {"recipient": true}})}
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	if err := crewroot.WriteSync("team", crewroot.SyncBinding{Mode: "relay", Side: "server", Editing: "local", PairID: "pair", PeerProjectID: "local"}); err != nil {
		t.Fatal(err)
	}
	var sender string
	for id, a := range team.Agents {
		if a.OwnerID == "owner" && a.Tab == "sender" {
			sender = id
		}
	}
	c := agentConversation{ID: "inbox-paired", Next: 1, Endpoints: [2]agentMessageEndpoint{
		{UserID: "owner", Kind: triggerCallerCrew, ID: "team", Profile: "work", ChatKey: "team:chat:sender", TeamAgentID: sender, Session: "old-session"},
		{UserID: "other", Kind: triggerCallerCrew, ID: "team", Profile: "work", ChatKey: "team:chat:recipient", Session: "other-session"},
	}, Messages: []agentMessage{{ID: "msg-reply", Sequence: 1, From: 1, Text: "Visible reply", SentAt: time.Now(), Delivery: "delivered"}}}
	agentMessageMu.Lock()
	store, err := readAgentMessageStore(t.Context())
	if err == nil {
		store.Conversations = append(store.Conversations, c)
		err = saveAgentMessageStore(t.Context(), store)
	}
	agentMessageMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	read := func(user, pair, tab string) (int, string) {
		r := httptest.NewRequest("POST", "/", nil).WithContext(context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: user}))
		w := httptest.NewRecorder()
		api.crewTeamMessage(w, r, "team", crewSyncRequest{Action: "team_message", PairID: pair, TeamMessage: &crewTeamMessageRequest{Operation: "read", FromTab: tab, ConversationID: "current", AgentID: c.ID}})
		return w.Code, w.Body.String()
	}
	if status, body := read("owner", "pair", "sender"); status != 200 || !strings.Contains(body, "Visible reply") {
		t.Fatalf("paired read: %d %s", status, body)
	}
	for _, v := range [][3]string{{"owner", "wrong", "sender"}, {"owner", "pair", "sibling"}, {"other", "pair", "sender"}} {
		if status, body := read(v[0], v[1], v[2]); status == 200 || strings.Contains(body, "Visible reply") {
			t.Fatalf("foreign endpoint read: %d %s", status, body)
		}
	}
	a := team.Agents[sender]
	a.InTeam = false
	team.Agents[sender] = a
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	if status, body := read("owner", "pair", "sender"); status == 200 || strings.Contains(body, "Visible reply") {
		t.Fatalf("revoked endpoint read: %d %s", status, body)
	}
}

// Exact names select an admitted registry row; ambiguous names and near UUIDs
// are never silently corrected to another agent.
func TestCrewTeamMessageNamesStayInsideAdmittedRegistry(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	newExternalCrewCommandFixture(t)
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
