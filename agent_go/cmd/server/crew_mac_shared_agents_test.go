package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-856: a publicly listed Mac agent refused attached chats because they carry messaging permissions,
// not Crew tool permissions. Both sending and reading a reply must use the current shared-agent audience.
func TestMacSharedAgentMessagingConnectionUsesCurrentAudience(t *testing.T) {
	owners := withProjectOwnerRegistry(t)
	if err := owners.Register(projectOwnerRecord{Product: "work", Folder: "crew", ProjectID: "p", OwnerID: "owner"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_PRODUCTS_AVAILABLE_TO_ALL", "")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","products":["work"]},{"id":"member","products":["work"]},{"id":"outsider","products":["work"]},{"id":"disabled","disabled":true,"products":["work"]},{"id":"no-crew","products":["brain"]}]}`)
	if err := crewroot.WriteSync("p", crewroot.SyncBinding{Side: "server", Editing: "local", Mode: "relay", PairID: "pair-test", PeerProjectID: "local-project"}); err != nil {
		t.Fatal(err)
	}
	if !crewRelayTwin("p") {
		t.Fatal("fixture must be a valid Mac Crew twin")
	}
	if err := writeCrewTeam(crewTeam{ProjectID: "p", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "member"}}}); err != nil {
		t.Fatal(err)
	}
	agents := []macSharedAgent{{Tab: "main", Audience: "everyone"}, {Tab: "team01", Audience: "team"}, {Tab: "chosen01", Audience: "chosen", People: []string{"member"}}}
	if err := writeMacSharedAgents("p", agents); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	for _, tc := range []struct {
		name, user, tab      string
		scopes               []string
		bound, send, allowed bool
	}{
		{"public send", "outsider", "main", []string{"messages:send", "messages:read"}, true, true, true},
		{"public reply read", "outsider", "main", []string{"messages:read"}, true, false, true},
		{"team send", "member", "team01", []string{"messages:send"}, true, true, true},
		{"chosen reply read", "member", "chosen01", []string{"messages:read"}, true, false, true},
		{"outside team", "outsider", "team01", []string{"messages:send"}, true, true, false},
		{"not chosen", "outsider", "chosen01", []string{"messages:read"}, true, false, false},
		{"private tab", "member", "private01", []string{"messages:read"}, true, false, false},
		{"read permission cannot send", "member", "main", []string{"messages:read"}, true, true, false},
		{"disabled caller", "disabled", "main", []string{"messages:send"}, true, true, false},
		{"no Crew product", "no-crew", "main", []string{"messages:send"}, true, true, false},
		{"unbound Crew permission", "outsider", "main", []string{"crews:run"}, false, true, true},
		{"unbound messaging alone", "outsider", "main", []string{"messages:send"}, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := &accesstokens.Token{Scopes: tc.scopes}
			if tc.bound {
				token.BoundAgentID = "attached-agent"
			}
			peer := agentMessageEndpoint{Kind: triggerCallerCrew, ID: "p", UserID: "owner", ChatKey: macSharedAgentChatKey("p", tc.tab)}
			err := api.externalAuthorizeMessagePeer(t.Context(), &UserClaims{UserID: tc.user, AccessToken: token}, peer, tc.send)
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%t, error=%v", tc.allowed, err)
			}
		})
	}
	// PLAT-856-536: the Mac answered, but native delivery/validation held its reply
	// because this shared Crew endpoint has no named-agent ShareID.
	store := agentMessageStore{}
	now := time.Now().UTC()
	reply := func(user, tab string) agentConversation {
		return agentConversation{Endpoints: [2]agentMessageEndpoint{
			{UserID: user, Kind: triggerCallerUser, External: true, NamedAgentID: "attached-agent"},
			{UserID: "owner", Kind: triggerCallerCrew, ID: "p", ChatKey: macSharedAgentChatKey("p", tab)},
		}}
	}
	for _, tc := range []struct {
		user, tab string
		allowed   bool
	}{
		{"outsider", "main", true}, {"member", "team01", true}, {"member", "chosen01", true},
		{"outsider", "team01", false}, {"outsider", "chosen01", false}, {"member", "private01", false},
		{"disabled", "main", false}, {"no-crew", "main", false},
	} {
		t.Run("reply/"+tc.user+"/"+tc.tab, func(t *testing.T) {
			if got := store.shareSenderAllowed(reply(tc.user, tc.tab), 1, tc.user, now); got != tc.allowed {
				t.Fatalf("reply allowed=%t, want %t", got, tc.allowed)
			}
		})
	}
	public := reply("member", "main")
	if store.shareSenderAllowed(public, 1, "outsider", now) {
		t.Fatal("a public audience let another person read this inbox's reply")
	}
	for _, change := range []struct {
		name string
		edit func(*agentMessageEndpoint)
	}{
		{"wrong owner", func(e *agentMessageEndpoint) { e.UserID = "outsider" }},
		{"external impersonation", func(e *agentMessageEndpoint) { e.External = true }},
		{"guest impersonation", func(e *agentMessageEndpoint) { e.Guest = "outsider" }},
		{"no chat", func(e *agentMessageEndpoint) { e.ChatKey = "" }},
		{"mailbox is not an agent tab", func(e *agentMessageEndpoint) { e.ChatKey = "messages:main" }},
	} {
		t.Run("reply/"+change.name, func(t *testing.T) {
			forged := public
			change.edit(&forged.Endpoints[1])
			if store.shareSenderAllowed(forged, 1, "member", now) {
				t.Fatal("invalid shared-agent endpoint admitted")
			}
		})
	}
	// An already-open inbox does not retain access after its agent is unshared.
	if err := writeMacSharedAgents("p", nil); err != nil {
		t.Fatal(err)
	}
	if store.shareSenderAllowed(public, 1, "member", now) {
		t.Fatal("a queued reply retained its revoked audience")
	}
	for _, send := range []bool{true, false} {
		claims := &UserClaims{UserID: "member", AccessToken: &accesstokens.Token{BoundAgentID: "attached-agent", Scopes: []string{"messages:send", "messages:read"}}}
		if err := api.externalAuthorizeMessagePeer(t.Context(), claims, agentMessageEndpoint{Kind: triggerCallerCrew, ID: "p", UserID: "owner", ChatKey: "p"}, send); err == nil {
			t.Fatalf("revoked audience still admitted send=%t", send)
		}
	}
}

// PLAT-856-509: an agent shared from a Mac Crew. The server decides who is in its audience; the Mac refuses a tab it
// does not share; a sender reads only their own exchange, never another sender's turns or the owner's own.
func TestMacSharedAgentAudienceAndPrivateExchange(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	agents, err := cleanMacSharedAgents([]macSharedAgent{
		{Tab: "writer01", Name: "Writer", Purpose: "Drafts posts", Icon: "✍️", Audience: "chosen", People: []string{"a", "c"}},
		{Tab: "helper01", Name: "Helper", Purpose: "Answers questions", Icon: "💬", Audience: "everyone"},
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
		if _, reason := macSharedAudience(readMacSharedAgents("p"), tc.tab, tc.caller, "owner", []string{"a", "c"}); reason != tc.reason {
			t.Fatalf("%s: reason %q, want %q", tc.name, reason, tc.reason)
		}
	}
	if _, err = cleanMacSharedAgents([]macSharedAgent{{Tab: "../x", Name: "x", Purpose: "x", Icon: "💬", Audience: "everyone"}}, nil); err == nil {
		t.Fatal("a tab id with path characters was saved")
	}
	// Owner, 4 Nov: an agent is shared only with its name, purpose and icon.
	if _, err = cleanMacSharedAgents([]macSharedAgent{{Tab: "helper01", Name: "Helper", Purpose: "Answers questions", Audience: "everyone"}}, nil); err == nil {
		t.Fatal("an agent without an icon was shared")
	}
	// An agent already shared before the rule keeps working when its name or purpose changes; a new audience
	// counts as sharing again and needs the icon.
	shared := []macSharedAgent{{Tab: "helper01", Name: "Helper", Purpose: "Answers questions", Audience: "everyone"}}
	if _, err = cleanMacSharedAgents([]macSharedAgent{{Tab: "helper01", Name: "Helper", Purpose: "Answers anything", Audience: "everyone"}}, shared); err != nil {
		t.Fatalf("refreshing an existing share without an icon was refused: %v", err)
	}
	if _, err = cleanMacSharedAgents([]macSharedAgent{{Tab: "helper01", Name: "Helper", Purpose: "Answers questions", Audience: "team"}}, shared); err == nil {
		t.Fatal("a new audience for an agent without an icon was accepted")
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
}
