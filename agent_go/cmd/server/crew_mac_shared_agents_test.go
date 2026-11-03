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
	if len(senderOwnTurns(items, "")) != 0 {
		t.Fatal("an unnamed sender read the tab")
	}
}
