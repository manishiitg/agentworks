package server

import (
	"encoding/json"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// PLAT-856-509: an agent shared from a Mac Crew. The server decides who is in its audience; the Mac refuses a tab it
// does not share; a sender reads only their own exchange, never another sender's turns or the owner's own.
func TestMacSharedAgentAudienceAndPrivateExchange(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	agents, err := cleanMacSharedAgents([]macSharedAgent{
		{Tab: "writer01", Name: "Writer", Purpose: "Drafts posts", Audience: "chosen", People: []string{"a", "c"}},
		{Tab: "helper01", Name: "Helper", Purpose: "Answers questions", Audience: "everyone"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = writeMacSharedAgents("p", agents); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, tab, caller, reason string
	}{
		{"chosen person", "writer01", "a", ""},
		{"not chosen", "writer01", "b", "not_in_audience"},
		{"everyone", "helper01", "b", ""},
		{"owner uses their own chat", "writer01", "owner", "owner"},
		{"Main or an unshared tab", "p", "a", "not_shared"},
	} {
		if _, reason := macSharedAudience(readMacSharedAgents("p"), tc.tab, tc.caller, "owner"); reason != tc.reason {
			t.Fatalf("%s: reason %q, want %q", tc.name, reason, tc.reason)
		}
	}
	if _, err = cleanMacSharedAgents([]macSharedAgent{{Tab: "../x", Name: "x", Purpose: "x", Audience: "everyone"}}, nil); err == nil {
		t.Fatal("a tab id with path characters was saved")
	}

	// The Mac refuses before any turn when the tab is not one it shares, or the request is malformed.
	api := &StreamingAPI{}
	args, _ := json.Marshal(map[string]any{"sender_id": "a"})
	for _, tab := range []string{"other01", "../x"} {
		got := api.desktopMacSharedAgent(t.Context(), &UserClaims{UserID: "owner"}, agentprofiles.Profile{}, "p", crewChatCall{Operation: "agent_ask", Tab: tab, Message: "hi", Args: args})
		if got.Status != 403 {
			t.Fatalf("tab %q: the Mac took a message for a tab it does not share: %d", tab, got.Status)
		}
	}

	// a and c both message Writer, and the owner types in that tab too: a reads only a's turn and its answer.
	items := []codeTurnItem{
		{Seq: 1, Turn: "t1", Role: "you", SenderID: "a", Text: "from a"},
		{Seq: 2, Turn: "t1", Role: "assistant", Text: "answer to a"},
		{Seq: 3, Turn: "t2", Role: "you", SenderID: "c", Text: "from c"},
		{Seq: 4, Turn: "t2", Role: "assistant", Text: "answer to c"},
		{Seq: 5, Turn: "t3", Role: "you", Text: "owner's own words"},
		{Seq: 6, Turn: "t3", Role: "assistant", Text: "answer to the owner"},
		// c's message added into a's running turn (asks are queued, so this is only a backstop): neither reads it.
		{Seq: 7, Turn: "t4", Role: "you", SenderID: "a", Text: "a again"},
		{Seq: 8, Turn: "t4", Role: "you", SenderID: "c", Text: "c steered in"},
		{Seq: 9, Turn: "t4", Role: "assistant", Text: "mixed answer"},
	}
	got := senderOwnTurns(items, "a")
	if len(got) != 2 || got[0].Text != "from a" || got[1].Text != "answer to a" {
		t.Fatalf("a read more than its own exchange: %+v", got)
	}
	if got := senderOwnTurns(items, "c"); len(got) != 2 || got[0].Text != "from c" {
		t.Fatalf("c read a mixed turn or someone else's: %+v", got)
	}
	// Two messages from a, close together: each answer is read by its own message, never the other's.
	twice := []codeTurnItem{
		{Seq: 1, Turn: "q1", Role: "you", SenderID: "a", Submission: "msg-1", Text: "first"},
		{Seq: 2, Turn: "q2", Role: "you", SenderID: "a", Submission: "msg-2", Text: "second"},
		{Seq: 3, Turn: "q2", Role: "assistant", Text: "answer to second"},
		{Seq: 4, Turn: "q1", Role: "assistant", Text: "answer to first"},
	}
	if got := submissionTurn(senderOwnTurns(twice, "a"), "msg-1"); len(got) != 2 || got[1].Text != "answer to first" {
		t.Fatalf("msg-1 read another message's answer: %+v", got)
	}
	if len(senderOwnTurns(items, "")) != 0 {
		t.Fatal("an unnamed sender read the tab")
	}
}

// PLAT-856-509: a shared agent's address for other people's agents is unique for its owner, survives a rename, and
// stays reserved while it is off, so turning it back on gives the same address.
func TestMacSharedAgentAddressKeptAndReserved(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	if got := macAgentSlug("  Sales & Leads!! "); got != "sales-leads" {
		t.Fatalf("slug %q", got)
	}
	first := assignMacAgentAddresses(t.Context(), "owner", "p", []macSharedAgent{{Tab: "a1", Name: "Writer"}, {Tab: "a2", Name: "Writer"}}, nil)
	if first[0].Address != "writer" || first[1].Address != "writer-2" {
		t.Fatalf("addresses not unique: %+v", first)
	}
	renamed := assignMacAgentAddresses(t.Context(), "owner", "p", []macSharedAgent{{Tab: "a1", Name: "Editor"}}, first)
	if renamed[0].Address != "writer" {
		t.Fatalf("a rename changed the address: %+v", renamed)
	}
	if len(renamed) != 2 || !renamed[1].Off || renamed[1].Address != "writer-2" {
		t.Fatalf("a turned-off agent lost its reserved address: %+v", renamed)
	}
	again := assignMacAgentAddresses(t.Context(), "owner", "p", []macSharedAgent{{Tab: "a1", Name: "Editor"}, {Tab: "a2", Name: "Writer"}}, renamed)
	if again[1].Address != "writer-2" || again[1].Off {
		t.Fatalf("turning it back on did not reuse its address: %+v", again)
	}
}

// PLAT-856-509: sharing from chat names people in words. Only an exact name or email picks someone; any partial match,
// even a single one, goes back to the person as a question, never a guess.
func TestMatchNetworkPersonAsksWhenAmbiguous(t *testing.T) {
	people := []networkPerson{{ID: "1", Username: "anita", Email: "anita@example.com"}, {ID: "2", Username: "anil", Email: "anil.k@example.com"}, {ID: "3", Username: "rakesh", Email: "rakesh@example.com"}}
	for _, tc := range []struct{ text, want string }{{"Anita", "1"}, {"rakesh@example.com", "3"}, {"anil.k", "2"}, {"rak", ""}, {"ani", ""}, {"zoe", ""}} {
		got, err := matchNetworkPerson(people, tc.text)
		if got != tc.want || (tc.want == "") != (err != nil) {
			t.Fatalf("%q: got %q, %v; want %q", tc.text, got, err, tc.want)
		}
	}
}

// PLAT-856-509, decisions log 3 Nov: each person talks to a shared agent in their own thread, kept off the owner's
// tab strip, and that thread is not a user-view chat (which would put it back in Run mode). Their turns never get the
// owner-only tools.
func TestSharedAgentThreadPerSender(t *testing.T) {
	a, b := sharedThreadKey("p", "main", "tester-b"), sharedThreadKey("p", "main", "tester-c")
	if a == b || a == sharedThreadKey("p", "writer01", "tester-b") || a != sharedThreadKey("p", "main", "tester-b") {
		t.Fatalf("threads not one per agent and sender: %q %q", a, b)
	}
	if !hiddenFromTabStrip(a) || isUserViewConversationKey(a) || !isSideChatConversationKey(a) {
		t.Fatalf("thread %q: hidden=%t userView=%t side=%t", a, hiddenFromTabStrip(a), isUserViewConversationKey(a), isSideChatConversationKey(a))
	}
	for _, tool := range []string{"share", "manage_crew_sync", "settings", "set_workflow_secret"} {
		if !sharedAgentOwnerOnlyTools[tool] {
			t.Fatalf("%s is offered to someone else's turn", tool)
		}
	}
}
