package server

import (
	"strconv"
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
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	// Main is optional and must be an actual owner registry record (746-885).
	const mainKey = "team:chat:root-owner"
	f.write(t, externalCommandCrewState+"/product.json", `{"schema_version":1,"product":"work","id":"team","main_chat_key":"`+mainKey+`"}`)
	if err := defaultProductConversationRegistryStore().writeDocument(t.Context(), productConversationRegistryPath("owner"), productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{
		productConversationRegistryEntryKey(crewProfileID, mainKey): {ProfileID: crewProfileID, ConversationKey: mainKey, WorkspacePath: externalCommandCrewRoot, SessionID: "owner-main"},
	}}); err != nil {
		t.Fatal(err)
	}

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
	if q := got[1]; q.Status != "answered" || q.StatusLine != "Answered in 2 min" || q.Question != "check the login tests" || q.Answer != "41 passed" || q.From.Label != "Your Main" {
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
		if q.Question != "" || q.Topic != "" || q.Answer != "" || !strings.HasPrefix(q.HiddenLine, "Chat hidden by ") {
			t.Fatalf("a Hidden chat's text reached a teammate: %+v", q)
		}
	}
	// Its own person still reads it; the owner's chat stays Live.
	if q := read("other")[1]; q.Question != "check the login tests" || q.Answer != "41 passed" || q.HiddenLine != "" {
		t.Fatalf("the person who hid their chat lost the text: %+v", q)
	}
	// A stale question to an online server agent is not indefinitely "Waiting".
	team, _ := readCrewTeam("team")
	old := agentMessageStore{Conversations: []agentConversation{{Endpoints: [2]agentMessageEndpoint{ownerMain, memberAgent}, Messages: []agentMessage{{ID: "old", From: 0, Text: "private question", SentAt: time.Now().Add(-31 * time.Minute)}}}}}
	byKey := map[string]*crewTeamActivityAgent{
		"team":    {person: crewTeamAgentsPerson{UserID: "owner"}},
		serverKey: {person: crewTeamAgentsPerson{UserID: "other", Name: "Other"}, agent: crewTeamAgent{Place: "server", ChatKey: serverKey}},
	}
	rows := crewTeamActivityMessagesFrom(t.Context(), team, "owner", old, byKey)
	if len(rows) != 1 || rows[0].Status != "no_answer" || rows[0].Question != "" || rows[0].Topic != "" {
		t.Fatalf("30-minute state must preserve hidden text: %+v", rows)
	}

}

// PLAT-746-904: Show older follows a stable message ID, not an offset shifted by new messages.
func TestCrewTeamActivityPagesByMessageID(t *testing.T) {
	rows := make([]crewTeamActivityMessage, 21)
	for i := range rows {
		rows[i].ID = strconv.Itoa(i)
	}
	first, cursor := crewTeamActivityMessagePage(rows, "")
	if len(first) != 20 || cursor != "19" {
		t.Fatalf("wrong first page: %d %q", len(first), cursor)
	}
	rows = append([]crewTeamActivityMessage{{ID: "new"}}, rows...)
	second, next := crewTeamActivityMessagePage(rows, cursor)
	if len(second) != 1 || second[0].ID != "20" || next != "" {
		t.Fatalf("cursor shifted after a new question: %+v %q", second, next)
	}
}
