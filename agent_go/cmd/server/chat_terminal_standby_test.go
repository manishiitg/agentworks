package server

import (
	"context"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-856-569, Team Manager's decision: pause exactly the handed-off
// conversation, not the agent or a teammate's separate thread. The paired
// acknowledgement must fence an already-imported or lazily-imported copy.
func TestTerminalStandbyMarkerIsOwnerPairedAndConversationScoped(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	if e := crewroot.WriteSync("team", crewroot.SyncBinding{Mode: "relay", PairID: "owner-pair", Side: "server", Editing: "local", PeerProjectID: "mac-crew"}); e != nil {
		t.Fatal(e)
	}
	source := ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: "team:chat:helper01", ConversationID: "conversation-terminal-source", SessionID: "platform-source", WorkspacePath: "Crew/test-team", ResourceID: "team"}
	other := source
	other.ConversationKey = sharedThreadKey("team", "helper01", "other")
	other.ConversationID = "conversation-other-thread"
	other.SessionID = "platform-other"
	registry := defaultProductConversationRegistryStore()
	doc := productConversationRegistryDocument{Version: productConversationRegistryVersion, Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, source.ConversationKey): source, productConversationRegistryEntryKey(crewProfileID, other.ConversationKey): other}}
	ctx := context.Background()
	if e := registry.writeDocument(ctx, productConversationRegistryPath("owner"), doc); e != nil {
		t.Fatal(e)
	}
	marker := chatTerminalStandby{Peer: "mac-crew", Key: "mac-crew:chat:helper01", Conversation: source.ConversationID, State: chatTerminalState{Owner: "local-user", Session: "mac-session", Receipt: "receipt", Native: "11111111-1111-4111-8111-111111111111", Out: true, Revision: 1}}
	call := func(claims *UserClaims, pair string, m chatTerminalStandby) error {
		_, e := f.api.crewSync(context.WithValue(ctx, UserContextKey, claims), claims, "team", crewSyncRequest{Action: "terminal_marker", PairID: pair, Terminal: &m})
		return e
	}
	owner := &UserClaims{UserID: "owner"}
	if e := call(&UserClaims{UserID: "other"}, "owner-pair", marker); e == nil {
		t.Fatal("peer marked another person's conversation")
	}
	if e := call(owner, "wrong-pair", marker); e == nil {
		t.Fatal("unpaired Mac marked conversation")
	}
	if e := call(&UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, CrewIDs: []string{"team"}}}, "owner-pair", marker); e == nil {
		t.Fatal("read grant marked conversation")
	}
	if e := call(owner, "owner-pair", marker); e != nil {
		t.Fatal(e)
	}
	if chatTerminalTurnRefusal(source.SessionID) == "" || chatTerminalTurnRefusal(other.SessionID) != "" {
		t.Fatal("mirror missed source or paused another conversation")
	}
	if got := terminalStandbyCallRefusal("team", "owner", "helper01", "other", true); got != chatTerminalPeerNotice {
		t.Fatalf("Team message lacks plain reason: %q", got)
	}
	if got := terminalStandbyCallRefusal("team", "owner", "helper01", "other", false); got != "" {
		t.Fatal("a teammate's independent thread was paused")
	}
	if f.api.crewStandbyRoute(ctx, "owner", "team", "helper01", "other", true, true) {
		t.Fatal("standby took over terminal-out source")
	}
	if label, _ := crewAlwaysOnPlaceLabel("team", "owner", "helper01", false, false, true); label != "In terminal" {
		t.Fatal("presence missing", label)
	}
	// Import may assign a new platform session ID; the stable conversation fence follows it.
	source.SessionID = "newly-imported-platform"
	doc.Entries[productConversationRegistryEntryKey(crewProfileID, source.ConversationKey)] = source
	if e := registry.writeDocument(ctx, productConversationRegistryPath("owner"), doc); e != nil {
		t.Fatal(e)
	}
	if e := applyImportedTerminalStandby(ctx, "team", "owner", source.ConversationID); e != nil {
		t.Fatal(e)
	}
	if chatTerminalTurnRefusal(source.SessionID) == "" {
		t.Fatal("lazy imported session escaped fence")
	}
	ended := marker
	ended.State.Out = false
	ended.State.Revision = 2
	if e := call(owner, "owner-pair", ended); e != nil {
		t.Fatal(e)
	}
	if chatTerminalTurnRefusal(source.SessionID) != "" {
		t.Fatal("cleared marker still fences source")
	}
	if e := call(owner, "owner-pair", marker); e == nil {
		t.Fatal("stale Out update resurrected old handoff")
	}
	f.api.activeSessions = map[string]*ActiveSessionInfo{source.SessionID: {Status: "running"}}
	marker.State.Revision = 3
	if e := call(owner, "owner-pair", marker); e == nil {
		t.Fatal("marked an active server copy Out")
	}
	if chatTerminalTurnRefusal(source.SessionID) != "" {
		t.Fatal("refused handoff changed live copy")
	}
}
