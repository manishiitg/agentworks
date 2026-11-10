package server

import (
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Team activity log, access rule: a message between two Team agents shows its text only while both agents' chats are
// Live for the viewer (Hidden by the other person: only the line), a reply marks the question answered, a question to
// an agent whose Mac is off says so, and what a person typed to their own agent is never listed.
func TestCrewTeamActivityHidesTextOfHiddenChatsAndMarksAnswers(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	if err := crewroot.WriteSync("team", crewroot.SyncBinding{Mode: "relay", Side: "server", Editing: "local", PairID: "owner-pair", PeerProjectID: "mac-crew"}); err != nil {
		t.Fatal(err)
	}
	if err := writeCrewTeam(crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}}}); err != nil {
		t.Fatal(err)
	}
	if err := writeMacSharedAgents("team", []macSharedAgent{{Tab: macMainTab, Audience: "team"}}); err != nil {
		t.Fatal(err)
	}
	const serverKey = "team:chat:agent1"
	doc := productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, serverKey): {ProfileID: crewProfileID, ConversationKey: serverKey, SessionID: "other-session"}}}
	if err := defaultProductConversationRegistryStore().writeDocument(t.Context(), productConversationRegistryPath("other"), doc); err != nil {
		t.Fatal(err)
	}
	// The owner's Mac Main (as its Mac sends: the Crew's id) and the member's server agent; a person's own message too.
	ownerMain := agentMessageEndpoint{UserID: "owner", Kind: triggerCallerCrew, Profile: crewProfileID, ID: "team", ChatKey: "team"}
	memberAgent := agentMessageEndpoint{UserID: "other", Kind: triggerCallerCrew, Profile: crewProfileID, ID: "team", ChatKey: serverKey, Session: "other-session"}
	asked := time.Now().Add(-time.Hour).UTC()
	store := agentMessageStore{Conversations: []agentConversation{
		{ID: "inbox-agents", Endpoints: [2]agentMessageEndpoint{ownerMain, memberAgent}, Next: 4, Messages: []agentMessage{
			{ID: "q1", Sequence: 1, From: 0, Text: "check the login tests", SentAt: asked, Delivery: "delivered"},
			{ID: "a1", Sequence: 2, From: 1, Text: "41 passed", SentAt: asked.Add(2 * time.Minute), Delivery: "delivered"},
			{ID: "q2", Sequence: 3, From: 1, Text: "which page for sign-up", SentAt: asked.Add(5 * time.Minute), Delivery: "stored"},
		}},
		{ID: "inbox-person", Endpoints: [2]agentMessageEndpoint{{UserID: "other", Kind: triggerCallerUser, ID: "other", External: true}, memberAgent}, Next: 2, Messages: []agentMessage{
			{ID: "typed", Sequence: 1, From: 0, Text: "my own words", SentAt: asked, Delivery: "delivered"},
		}},
	}}
	if err := saveAgentMessageStore(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	read := func(viewer string) []crewTeamActivityMessage {
		team, err := readCrewTeam("team")
		if err != nil {
			t.Fatal(err)
		}
		return crewTeamActivityFor(t.Context(), team, viewer).Messages
	}

	got := read("owner")
	if len(got) != 2 || got[0].ID != "q2" || got[1].ID != "q1" {
		t.Fatalf("want the two agent questions newest first, not the person's own message: %+v", got)
	}
	if q := got[1]; q.Status != "answered" || q.StatusLine != "answered in 2 min" || q.Question != "check the login tests" || q.Answer != "41 passed" || q.From.Label != "Your Main" {
		t.Fatalf("answered question with both chats Live: %+v", q)
	}
	if q := got[0]; q.Status != "mac_off" || q.StatusLine != "not answered — your Mac is off" || q.Answer != "" {
		t.Fatalf("a question to the owner's Main with the Mac off: %+v", q)
	}

	off := crewChatShareAgent
	if _, err := setCrewTeamAgentRule(t.Context(), "team", "other", serverKey, nil, nil, &off); err != nil {
		t.Fatal(err)
	}
	for _, q := range read("owner") {
		if q.Question != "" || q.Answer != "" || !strings.HasPrefix(q.HiddenLine, "Chat hidden by ") {
			t.Fatalf("a Hidden chat's text reached a teammate: %+v", q)
		}
	}
	// Its own person still reads it; the owner's chat stays Live.
	if q := read("other")[1]; q.Question != "check the login tests" || q.Answer != "41 passed" || q.HiddenLine != "" {
		t.Fatalf("the person who hid their chat lost the text: %+v", q)
	}
}
