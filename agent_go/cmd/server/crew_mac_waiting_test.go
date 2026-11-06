package server

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-856-509: the actual relay and mailbox hold an offline ask, deliver it on
// reconnect and put its answer in the original inbox. Revocation stops a queued ask.
// The Mac transport replies are a fixture; this does not run a native AI program.
func TestMacWaitingReconnectRepliesToOriginalInboxAndRechecksAccess(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]},{"id":"other","products":["work"]},{"id":"removed","products":["work"]}]}`)
	f.api.projectActions = NewProjectActionDispatcher(f.api, f.api.agentProfiles)
	if err := crewroot.WriteSync("team", crewroot.SyncBinding{Side: "server", Editing: "local", Mode: "relay", PairID: "pair", PeerProjectID: "local-team"}); err != nil {
		t.Fatal(err)
	}
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}, {UserID: "removed"}}}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	if err := writeMacSharedAgents("team", []macSharedAgent{{Tab: "main", Name: "Main", Audience: "team"}}); err != nil {
		t.Fatal(err)
	}
	// The watched Main tab uses its physical key, not the AgentBridge address.
	mainKey := originalMainChatKey("team", "main-conversation")
	f.write(t, externalCommandCrewState+"/product.json", `{"schema_version":1,"product":"work","id":"team","title":"Test Team","session_id":"sess-team","main_chat_key":"`+mainKey+`"}`)
	registry := defaultProductConversationRegistryStore()
	document, err := registry.loadDocument(t.Context(), productConversationRegistryPath("owner"))
	if err != nil {
		t.Fatal(err)
	}
	document.Entries[productConversationRegistryEntryKey(crewProfileID, mainKey)] = ProductConversationRecord{ProfileID: crewProfileID, ResourceID: "team", ConversationKey: mainKey, ConversationID: "main-conversation", SessionID: "main-session", WorkspacePath: externalCommandCrewRoot}
	if err := registry.writeDocument(t.Context(), productConversationRegistryPath("owner"), document); err != nil {
		t.Fatal(err)
	}
	// Live test, 6 Nov: a teammate's agent addressed the owner's Main by its root key and was refused as not shared.
	// The owner's own root record is Main; anyone else's root key is not.
	if tab := macSharedAgentTab("team", mainKey); tab != macMainTab {
		t.Fatalf("the owner's Main under its root key must route as Main, got %q", tab)
	}
	if tab := macSharedAgentTab("team", originalMainChatKey("team", "someone-elses-conversation")); tab == macMainTab {
		t.Fatal("a root key that is not the owner's own record routed as the owner's Main")
	}
	// PLAT-856-509: the owner's Main under its root key is the Mac's Main, so it waits for the Mac like the Crew's own key.
	// Live test, 6 Nov: a side agent's key that the owner's registry also holds was routed to Main, so a website turn for
	// the ChatGPT tab ran as Main's Claude session.
	sideKey := "team" + codeChatSideMarker + "0986645f"
	document.Entries[productConversationRegistryEntryKey(crewProfileID, sideKey)] = ProductConversationRecord{ProfileID: crewProfileID, ResourceID: "team", ConversationKey: sideKey, ConversationID: "side-conversation"}
	if err := registry.writeDocument(t.Context(), productConversationRegistryPath("owner"), document); err != nil {
		t.Fatal(err)
	}
	if tab := crewStandbyTabForKey("team", sideKey); tab != "0986645f" {
		t.Fatalf("a registered side agent routed as %q, not its own tab", tab)
	}
	if tab := crewStandbyTabForKey("team", mainKey); tab != macMainTab {
		t.Fatalf("the owner's Main under its root key must be Main, got %q", tab)
	}
	// Main off (owner, 6 Nov): a message to the Crew itself reaches the first shared agent, not an unshared Main. A key
	// naming Main stays Main, and a Main public on AgentBridge is still shared.
	mainOff := team
	mainOff.AgentChoices = map[string]map[string]bool{"owner": {macMainTab: false}}
	if err := writeCrewTeam(mainOff); err != nil {
		t.Fatal(err)
	}
	withHelper := []macSharedAgent{{Tab: "main", Name: "Main", Audience: "team"}, {Tab: "helper01", Name: "Helper", Audience: "team"}}
	if err := writeMacSharedAgents("team", withHelper); err != nil {
		t.Fatal(err)
	}
	if tab := macSharedAgentTab("team", "team"); tab != "helper01" {
		t.Fatalf("a message to the Crew with Main off went to %q, want the first shared agent", tab)
	}
	if tab := macSharedAgentTab("team", mainKey); tab != macMainTab {
		t.Fatalf("a key naming Main routed to %q", tab)
	}
	withHelper[0].AgentBridgePublic = true
	if err := writeMacSharedAgents("team", withHelper); err != nil {
		t.Fatal(err)
	}
	if tab := macSharedAgentTab("team", "team"); tab != macMainTab {
		t.Fatalf("a Main public on AgentBridge lost the Crew's messages to %q", tab)
	}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	if err := writeMacSharedAgents("team", []macSharedAgent{{Tab: "main", Name: "Main", Audience: "team"}}); err != nil {
		t.Fatal(err)
	}
	if tab := macSharedAgentTab("team", "team"); tab != macMainTab {
		t.Fatalf("a message to the Crew with Main shared went to %q", tab)
	}
	store := agentMessageStore{}
	for _, user := range []string{"other", "removed"} {
		store.Agents = append(store.Agents, namedMessageAgent{ID: "agent-" + user, Name: user, UserID: user})
		store.Conversations = append(store.Conversations, agentConversation{ID: "inbox-" + user, Next: 1,
			Endpoints: [2]agentMessageEndpoint{
				{UserID: user, Kind: triggerCallerUser, ID: user, External: true, NamedAgentID: "agent-" + user},
				{UserID: "owner", Kind: triggerCallerCrew, Profile: "work", ID: "team", Path: externalCommandCrewRoot, ChatKey: "team"},
			}, Messages: []agentMessage{{ID: "ask-" + user, From: 0, Sequence: 1, Text: "answer this", SentAt: time.Now(), Delivery: "delivered"}},
		})
	}
	if err := saveAgentMessageStore(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	for _, c := range store.Conversations {
		if err := f.api.deliverToMacSharedAgent(t.Context(), c, c.Messages[0], c.Endpoints[0], c.Endpoints[1]); err != nil {
			t.Fatal(err)
		}
	}
	waitingCount := func() int {
		t.Helper()
		s, err := readCrewMacWaiting("team")
		if err != nil {
			t.Fatal(err)
		}
		return len(s.Waiting)
	}
	if got := waitingCount(); got != 2 {
		t.Fatalf("offline asks not retained: %d", got)
	}
	// A broken record is not evidence of revocation: retain the original asks.
	sharedPath, _ := macSharedAgentsPath("team")
	sharedBytes, _ := os.ReadFile(sharedPath)
	if err := os.WriteFile(sharedPath, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	f.api.deliverCrewMacWaiting("owner", "team", "pair")
	if waitingCount() != 2 {
		t.Fatal("transient record failure discarded waiting mail")
	}
	if err := os.WriteFile(sharedPath, sharedBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if people := teamAgentsByPerson(t.Context(), team, "other"); len(people) == 0 || len(people[0].Agents) == 0 || people[0].Agents[0].StatusLabel == "" || people[0].Agents[0].ChatKey != mainKey {
		t.Fatal("disconnected Mac agent was not marked offline for its teammate")
	}
	team.People = team.People[:1]
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	// The Team list reads the Mac's tabs once it is back, and nobody answers this fixture's Mac until the loop below.
	crewMacTeamTabsCache.Store("team\x00owner", crewMacTeamTabsRead{at: time.Now()})
	commands := make(chan crewRelayMessage, 8)
	sock, err := f.api.crewRelayRegister(t.Context(), &UserClaims{UserID: "owner", Username: "owner"}, crewRelayMessage{Type: "hello", DeviceID: "mac", Crews: []crewRelayCrew{{Project: "team", Pair: "pair"}}}, func(m crewRelayMessage) error { commands <- m; return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sock.unregister()
	if people := teamAgentsByPerson(t.Context(), team, "other"); len(people) == 0 || len(people[0].Agents) == 0 || people[0].Agents[0].StatusLabel != "" {
		t.Fatal("reconnected Mac agent is still marked offline")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	for {
		select {
		case m := <-commands:
			if m.Call == nil {
				continue
			}
			var args struct {
				Sender string `json:"sender_id"`
			}
			_ = json.Unmarshal(m.Call.Args, &args)
			if args.Sender != "other" {
				t.Fatalf("revoked sender reached the Mac: %s", args.Sender)
			}
			body := json.RawMessage(`{"ok":true}`)
			if m.Call.Operation == "agent_read" {
				body = json.RawMessage(`{"working":false,"messages":[{"role":"assistant","text":"answer from original Mac"}]}`)
			}
			f.api.crewRelayHandle(ctx, sock, crewRelayMessage{Type: "ack", Result: &crewChatResult{ID: m.Call.ID, Status: 200, Body: body}})
		case <-time.After(20 * time.Millisecond):
			s, err := readAgentMessageSnapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			answered, refused := false, false
			for _, c := range s.Conversations {
				for _, m := range c.Messages {
					if c.ID == "inbox-removed" && m.From == 1 && strings.Contains(m.Text, "isn't shared with you anymore") {
						refused = true
					}
					if m.Text == "answer from original Mac" {
						if c.ID != "inbox-other" || m.From != 1 || m.Delivery != "stored" {
							t.Fatalf("answer routed incorrectly: %+v", m)
						}
						answered = true
					}
				}
			}
			if answered && refused && waitingCount() == 0 {
				return
			}
		case <-ctx.Done():
			t.Fatalf("queued ask did not complete its round trip: %v", ctx.Err())
		}
	}
}
