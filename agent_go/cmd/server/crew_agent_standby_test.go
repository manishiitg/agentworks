package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// PLAT-856 Always on, step 1: the server keeps an In team agent's standby copy from its owner's Mac through the
// normal sync request, admitted only for the owner on the Crew's own pair. A resend of the same copy changes nothing,
// a copy that does not extend the stored one keeps both, and turning an agent off drops its copy (Main, still In
// team, stays eligible).
func TestCrewStandbyCopyStoredUnchangedDivergedAndOwnerOnly(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	api := f.api
	const tab, key = "helper01", "mac-crew:chat:helper01"
	if err := crewroot.WriteSync("team", crewroot.SyncBinding{Mode: "relay", PairID: "owner-pair", Side: "server", Editing: "local", PeerProjectID: "mac-crew"}); err != nil {
		t.Fatalf("pair fixture: %v", err)
	}
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true}
	if err := writeCrewTeam(team); err != nil {
		t.Fatalf("team fixture: %v", err)
	}
	if err := writeMacSharedAgents("team", []macSharedAgent{{Tab: "main", Name: "Main", Audience: "team"}, {Tab: tab, Name: "Helper", Audience: "team"}}); err != nil {
		t.Fatalf("shared agents fixture: %v", err)
	}
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	ctx := context.WithValue(t.Context(), UserContextKey, owner)
	roots, err := api.crewSyncRoots(ctx, owner, "team")
	if err != nil {
		t.Fatalf("owner Crew roots: %v", err)
	}
	const native = "0f8fad5b-d9cb-469f-a165-70867728950e"
	// What desktopCrewNativeSync leaves on the server: the journal in this session's native stage.
	staged := func(journal string) *sessiontransfer.Bundle {
		t.Helper()
		sum := sha256.Sum256([]byte(journal))
		b := &sessiontransfer.Bundle{Provider: "claude-code", Format: "claude-jsonl", Transport: "structured", SessionID: native, Workspace: "/work/crew",
			Files: map[string]sessiontransfer.File{"session.jsonl": {Size: int64(len(journal)), SHA256: hex.EncodeToString(sum[:])}}}
		stage, err := crewNativeStage(roots, b)
		if err == nil {
			err = os.WriteFile(filepath.Join(stage, "session.jsonl"), []byte(journal), 0o600)
		}
		if err != nil {
			t.Fatalf("native stage: %v", err)
		}
		return b
	}
	store := func(claims *UserClaims, pair, journal string) (*crewStandbyReply, error) {
		t.Helper()
		item := crewStandbySendItem{Key: key, Conversation: &crewHandoffConversation{ConversationID: "conversation-helper", Native: staged(journal)}}
		out, err := api.crewSync(context.WithValue(t.Context(), UserContextKey, claims), claims, "team", crewSyncRequest{Action: "standby_store", PairID: pair, Standby: &crewStandbyRequest{Tab: tab, Sessions: []crewStandbySendItem{item}}})
		return out.Standby, err
	}
	first := `{"sessionId":"` + native + `","n":1}` + "\n"
	second := first + `{"sessionId":"` + native + `","n":2}` + "\n"
	other := `{"sessionId":"` + native + `","n":"rewritten"}` + "\n"

	if reply, err := store(owner, "owner-pair", first); err != nil || reply == nil || !reply.Eligible || reply.Event != "stored" || reply.Revision != 1 {
		t.Fatalf("first copy: %+v %v", reply, err)
	}
	if reply, err := store(owner, "owner-pair", first); err != nil || reply == nil || reply.Event != "unchanged" || reply.Revision != 1 {
		t.Fatalf("an unchanged resend must be a no-op: %+v %v", reply, err)
	}
	if reply, err := store(owner, "owner-pair", second); err != nil || reply == nil || reply.Event != "stored" || reply.Revision != 2 {
		t.Fatalf("an extended copy: %+v %v", reply, err)
	}
	if reply, err := store(owner, "owner-pair", other); err != nil || reply == nil || reply.Event != "diverged" || reply.Revision != 3 {
		t.Fatalf("a copy that does not extend the stored one: %+v %v", reply, err)
	}
	rows, err := readCrewStandby("team")
	if err != nil {
		t.Fatalf("standby record: %v", err)
	}
	row := rows[tab]
	session := row.Sessions[key]
	if row.Location != "standby" || row.PairID != "owner-pair" || len(session.Diverged) != 1 {
		t.Fatalf("standby record: %+v", row)
	}
	dir, _ := crewStandbyDir("team")
	for name, want := range map[string]string{session.Dir: other, session.Diverged[0]: second} {
		if got, err := os.ReadFile(filepath.Join(dir, tab, name, "native", "session.jsonl")); err != nil || string(got) != want {
			t.Fatalf("diverged copies must both be kept: %s %q %v", name, got, err)
		}
	}
	// Placement for Publish is untouched: the server must not treat a standby agent as published.
	if placements, err := readCrewAgentPlacements("team"); err != nil || len(placements) != 0 {
		t.Fatalf("standby wrote a publish placement: %+v %v", placements, err)
	}
	// Refusals come from the owner and pair rules (the same owner on the right pair is allowed above).
	if _, err := store(owner, "another-pair", second); err == nil {
		t.Fatal("a copy on another pair was stored")
	}
	if _, err := store(&UserClaims{UserID: "other", Username: "other"}, "owner-pair", second); err == nil {
		t.Fatal("someone other than the Crew's owner stored a copy")
	}
	if rows, err = readCrewStandby("team"); err != nil || rows[tab].Revision != 3 {
		t.Fatalf("a refused copy changed the record: %+v %v", rows[tab], err)
	}
	// The owner turns the helper off: it is Only me and its copy is dropped; Main stays In team and eligible.
	team.AgentChoices = map[string]map[string]bool{"owner": {tab: false}}
	if err := writeCrewTeam(team); err != nil {
		t.Fatalf("team update: %v", err)
	}
	if rows, err = readCrewStandby("team"); err != nil || len(rows) != 0 {
		t.Fatalf("an Only me agent kept its standby copy: %+v %v", rows, err)
	}
	if _, err := os.Stat(filepath.Join(dir, tab)); !os.IsNotExist(err) {
		t.Fatalf("an Only me agent's copy is still on the server: %v", err)
	}
	if ok, reason := crewStandbyEligible("team", "owner", "main"); !ok {
		t.Fatalf("Main must stay eligible (In team): %s", reason)
	}
}

// PLAT-856 Always on, step 2: with the owner's Mac on, an In team agent still answers on the Mac. With the Mac off, a
// teammate's message to it runs on the server from its standby copy, as the teammate (the asker the "When others ask"
// rule sees), and the server becomes its writer. An Only me agent is never answered from the server: it waits.
func TestCrewStandbyAnswersOnServerWhileMacOff(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	api := f.api
	const helper, solo = "helper01", "solo01"
	if err := crewroot.WriteSync("team", crewroot.SyncBinding{Mode: "relay", PairID: "owner-pair", Side: "server", Editing: "local", PeerProjectID: "mac-crew"}); err != nil {
		t.Fatalf("pair fixture: %v", err)
	}
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}},
		AgentChoices: map[string]map[string]bool{"owner": {solo: false}}}
	if err := writeCrewTeam(team); err != nil {
		t.Fatalf("team fixture: %v", err)
	}
	if err := writeMacSharedAgents("team", []macSharedAgent{{Tab: "main", Name: "Main", Audience: "team"}, {Tab: helper, Name: "Helper", Audience: "team"}, {Tab: solo, Name: "Solo", Audience: "team"}}); err != nil {
		t.Fatalf("shared agents fixture: %v", err)
	}
	// Both agents have a copy of their owner's chat (as step 1 leaves it); Solo is Only me.
	copyOf := func(tab string) crewAgentStandby {
		return crewAgentStandby{Tab: tab, Location: "standby", PairID: "owner-pair", Revision: 1, Sessions: map[string]crewAgentStandbySession{
			macSharedAgentChatKey("mac-crew", tab): {Dir: "copy", Fingerprint: "f", ConversationID: "conversation-" + tab, Provider: "claude-code", Revision: 1}}}
	}
	if err := writeCrewStandby("team", map[string]crewAgentStandby{helper: copyOf(helper), solo: copyOf(solo)}); err != nil {
		t.Fatalf("standby fixture: %v", err)
	}
	imported := []string{}
	crewStandbyImportForTest = func(owner, project, tab, sender string) error {
		imported = append(imported, tab+"/"+sender)
		return nil
	}
	var ran []crewChatCall
	crewStandbyRunForTest = func(_ context.Context, _ string, call crewChatCall) crewChatResult {
		ran = append(ran, call)
		return crewChatResult{Status: http.StatusOK, Body: []byte(`{"status":"accepted"}`)}
	}
	t.Cleanup(func() { crewStandbyImportForTest, crewStandbyRunForTest = nil, nil })

	// The owner's Mac is on: the Mac answers, nothing is imported or run here.
	crewMacSockets.Lock()
	crewMacSockets.byDevice["owner-mac"] = &crewMacSocket{owner: "owner", device: "owner-mac", twins: map[string]string{"team": "owner-pair"}, lastSeen: time.Now()}
	crewMacSockets.Unlock()
	t.Cleanup(func() { crewMacSockets.Lock(); delete(crewMacSockets.byDevice, "owner-mac"); crewMacSockets.Unlock() })
	if api.crewStandbyRoute(t.Context(), "owner", "team", helper, "other", true) || len(imported) != 0 {
		t.Fatalf("with the Mac on, the server took the agent (imported %v)", imported)
	}
	if label, tone := crewAlwaysOnPlaceLabel("team", "owner", helper, true, true, true); label != "Mac · synced to server" || tone != "ok" {
		t.Fatalf("Mac on label: %q %q", label, tone)
	}

	// The Mac goes off: the teammate asks Helper from their own AI app.
	crewMacSockets.Lock()
	delete(crewMacSockets.byDevice, "owner-mac")
	crewMacSockets.Unlock()
	if label, tone := crewAlwaysOnPlaceLabel("team", "owner", helper, false, true, true); label != "Mac off · answering from the server" || tone != "server" {
		t.Fatalf("In team, Mac off label: %q %q", label, tone)
	}
	member := &UserClaims{UserID: "other", Username: "other", AccessToken: &accesstokens.Token{Scopes: []string{"crews:run"}}}
	r := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(context.WithValue(t.Context(), UserContextKey, member))
	w := httptest.NewRecorder()
	api.macSharedAgentCall(w, r, "ask", map[string]any{"chat_id": macSharedAgentChatKey("team", helper), "message": "what changed today?"}, "owner", "team")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "submission_id") {
		t.Fatalf("the teammate's ask was not answered on the server: %d %s (held: %v)", w.Code, w.Body.String(), heldTabs(t))
	}
	if len(ran) != 1 || ran[0].Tab != helper || ran[0].Operation != "agent_ask" {
		t.Fatalf("the server did not run the ask: %+v", ran)
	}
	var args struct {
		SenderID string `json:"sender_id"`
	}
	if json.Unmarshal(ran[0].Args, &args) != nil || args.SenderID != "other" {
		t.Fatalf("the server turn must run with the teammate as the asker, got %q", args.SenderID)
	}
	if len(imported) != 1 || imported[0] != helper+"/other" {
		t.Fatalf("the owner's chat and the teammate's thread must come from the copy: %v", imported)
	}
	rows, err := readCrewStandby("team")
	if err != nil || rows[helper].Writer != "server" || rows[helper].WriterRevision != 1 || rows[helper].WriterSince == "" {
		t.Fatalf("the server must be recorded as the writer: %+v %v", rows[helper], err)
	}
	// From now on the teammate's thread runs here, not on the Mac.
	if !crewStandbyServesKey(sharedThreadKey("team", helper, "other")) || crewLivesOnMac(crewProfileID, sharedThreadKey("team", helper, "other"), "owner") {
		t.Fatal("the teammate's thread on a server-answered agent must run on the server")
	}

	// An Only me agent never answers from the server, even with a copy: it waits for the Mac.
	if api.crewStandbyRoute(t.Context(), "owner", "team", solo, "", true) || rows[solo].Writer != "" {
		t.Fatalf("an Only me agent was answered on the server: %+v", rows[solo])
	}
	if label, tone := crewAlwaysOnPlaceLabel("team", "owner", solo, false, true, false); label != "Mac off · waits for your Mac" || tone != "" {
		t.Fatalf("Only me, Mac off label: %q %q", label, tone)
	}
	if len(imported) != 1 {
		t.Fatalf("an Only me agent's copy was imported: %v", imported)
	}
}

func heldTabs(t *testing.T) []string {
	t.Helper()
	s, _ := readCrewMacWaiting("team")
	out := []string{}
	for _, held := range s.Waiting {
		out = append(out, held.Tab)
	}
	return out
}

// PLAT-856 Always on, step 3: once the owner's Mac is back and no server turn of the agent runs, the Mac takes the
// server's newer conversation (imported with the same native session id) and the agent is handed back (writer=mac). A
// teammate thread that starts on the server during the hand back holds the flip until it is taken too. When the Mac
// also changed the conversation, both versions are kept, the owner is told and nothing is imported. Only the owner on
// the Crew's own pair, with the Mac online, can take an agent back, over the sync request and the native pull alike.
func TestCrewHandbackTakesServerCopyKeepsBothAndRefusesOthers(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	api := f.api
	const tab, macKey, serverKey = "helper01", "mac-crew:chat:helper01", "team:chat:helper01"
	const native = "0f8fad5b-d9cb-469f-a165-70867728950e"
	if err := crewroot.WriteSync("team", crewroot.SyncBinding{Mode: "relay", PairID: "owner-pair", Side: "server", Editing: "local", PeerProjectID: "mac-crew"}); err != nil {
		t.Fatalf("pair fixture: %v", err)
	}
	if err := writeCrewTeam(crewTeam{ProjectID: "team", OwnerID: "owner", On: true}); err != nil {
		t.Fatalf("team fixture: %v", err)
	}
	if err := writeMacSharedAgents("team", []macSharedAgent{{Tab: tab, Name: "Helper", Audience: "team"}}); err != nil {
		t.Fatalf("shared agents fixture: %v", err)
	}
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	ctx := context.WithValue(t.Context(), UserContextKey, owner)
	roots, err := api.crewSyncRoots(ctx, owner, "team")
	if err != nil {
		t.Fatalf("owner Crew roots: %v", err)
	}
	serverWriter := func() {
		t.Helper()
		if err := writeCrewStandby("team", map[string]crewAgentStandby{tab: {Tab: tab, Location: "standby", PairID: "owner-pair", Revision: 1, Writer: "server", WriterRevision: 1,
			Sessions: map[string]crewAgentStandbySession{macKey: {Dir: "copy", ConversationID: "conversation-helper", Provider: "claude-code", Revision: 1, Imported: 1}}}}); err != nil {
			t.Fatalf("standby fixture: %v", err)
		}
	}
	serverWriter()
	// The server's own chat with the agent, as step 2 left it.
	addServerChat := func(key, session string) {
		t.Helper()
		store, path := defaultProductConversationRegistryStore(), productConversationRegistryPath("owner")
		doc, err := store.loadDocument(ctx, path)
		if err == nil {
			doc.Entries[productConversationRegistryEntryKey(crewProfileID, key)] = ProductConversationRecord{ConversationKey: key, ConversationID: "conversation-" + session, ProfileID: crewProfileID, SessionID: session, WorkspacePath: roots.Virtual, Provider: "claude-code"}
			err = store.writeDocument(ctx, path, doc)
		}
		if err != nil {
			t.Fatalf("server chat fixture: %v", err)
		}
	}
	addServerChat(serverKey, "srv-1")
	// A journal in dir, or in this session's native stage when dir is empty (where the server's capture leaves it).
	bundle := func(dir, journal string) *sessiontransfer.Bundle {
		t.Helper()
		sum := sha256.Sum256([]byte(journal))
		b := &sessiontransfer.Bundle{Provider: "claude-code", Format: "claude-jsonl", Transport: "structured", SessionID: native, Workspace: "/work/crew",
			Files: map[string]sessiontransfer.File{"session.jsonl": {Size: int64(len(journal)), SHA256: hex.EncodeToString(sum[:])}}, Root: dir}
		if dir == "" {
			stage, err := crewNativeStage(roots, b)
			if err != nil {
				t.Fatalf("native stage: %v", err)
			}
			b.Root = stage
		}
		if err := os.WriteFile(filepath.Join(b.Root, "session.jsonl"), []byte(journal), 0o600); err != nil {
			t.Fatalf("journal: %v", err)
		}
		return b
	}
	first := `{"sessionId":"` + native + `","n":1}` + "\n"
	serverJournal := first + `{"sessionId":"` + native + `","n":"server"}` + "\n"
	macJournal := first
	// The server's capture and the Mac's own version need a provider runtime; the test stands in for both.
	crewHandbackCaptureForTest = func(_ string, record ProductConversationRecord) (*crewHandoffConversation, error) {
		if record.ConversationKey != serverKey {
			return nil, nil // a thread with no saved turn yet
		}
		return &crewHandoffConversation{ConversationID: "conversation-helper", Native: bundle("", serverJournal)}, nil
	}
	crewHandbackMineForTest = func(_ crewroot.Roots, key string) (*ProductConversationRecord, *sessiontransfer.Bundle) {
		if key != macKey {
			return nil, nil
		}
		return &ProductConversationRecord{ConversationKey: key, SessionID: "mac-1"}, bundle(t.TempDir(), macJournal)
	}
	imported := []string{}
	threadKey := sharedThreadKey("team", tab, "other")
	crewHandbackImportForTest = func(_ crewroot.Roots, key string, c *crewHandoffConversation) error {
		imported = append(imported, key+"/"+c.Native.SessionID)
		if len(imported) == 1 {
			addServerChat(threadKey, "srv-thread") // a teammate's new ask runs on the server during the hand back
		}
		return nil
	}
	crewHandbackRetry = time.Millisecond
	t.Cleanup(func() {
		crewHandbackCaptureForTest, crewHandbackMineForTest, crewHandbackImportForTest = nil, nil, nil
		crewHandbackRetry = 15 * time.Second
	})
	call := func(claims *UserClaims, pair string) func(string, []crewStandbySendItem) (*crewStandbyReply, error) {
		return func(action string, sessions []crewStandbySendItem) (*crewStandbyReply, error) {
			out, err := api.crewSync(context.WithValue(t.Context(), UserContextKey, claims), claims, "team", crewSyncRequest{Action: action, PairID: pair, Standby: &crewStandbyRequest{Tab: tab, Sessions: sessions}})
			return out.Standby, err
		}
	}
	macRoots := roots
	macRoots.ID = "mac-crew"
	pull := func(b *sessiontransfer.Bundle) error { return bindCrewNativeStage(roots, b) } // one machine: the stage is shared
	handBack := func() (bool, int, error) {
		return api.runCrewHandback(t.Context(), "mac-crew", tab, macRoots, call(owner, "owner-pair"), pull, "owner-pair")
	}
	writer := func() string {
		t.Helper()
		rows, err := readCrewStandby("team")
		if err != nil {
			t.Fatalf("standby record: %v", err)
		}
		return rows[tab].Writer
	}

	// The Mac is still off: nothing changes, the server keeps answering.
	if done, _, err := handBack(); done || crewHandbackReason(err) != "mac_offline" || writer() != "server" {
		t.Fatalf("with the Mac off the agent must stay on the server: done=%t err=%v writer=%q", done, err, writer())
	}
	crewMacSockets.Lock()
	crewMacSockets.byDevice["owner-mac"] = &crewMacSocket{owner: "owner", device: "owner-mac", twins: map[string]string{"team": "owner-pair"}, lastSeen: time.Now()}
	crewMacSockets.Unlock()
	t.Cleanup(func() { crewMacSockets.Lock(); delete(crewMacSockets.byDevice, "owner-mac"); crewMacSockets.Unlock() })

	// Refusals come from the owner and pair rules, on the sync request and on the native pull.
	other := &UserClaims{UserID: "other", Username: "other"}
	if _, err := call(other, "owner-pair")("handback_pull", nil); err == nil {
		t.Fatal("someone other than the Crew's owner listed the agent's conversations")
	}
	if _, err := call(owner, "another-pair")("handback_pull", nil); err == nil {
		t.Fatal("another pair listed the agent's conversations")
	}
	for _, c := range []struct {
		claims *UserClaims
		pair   string
		want   int
	}{{other, "owner-pair", http.StatusForbidden}, {owner, "another-pair", http.StatusConflict}} {
		r := httptest.NewRequest(http.MethodPost, "/api/crew-sync/team/native/pull?pair="+c.pair, strings.NewReader("{}")).WithContext(context.WithValue(t.Context(), UserContextKey, c.claims))
		r = mux.SetURLVars(r, map[string]string{"project": "team", "op": "pull"})
		w := httptest.NewRecorder()
		api.handleCrewNativeSync(w, r)
		if w.Code != c.want {
			t.Fatalf("native pull by %s on %s: got %d, want %d (%s)", c.claims.UserID, c.pair, w.Code, c.want, w.Body.String())
		}
	}

	// A server turn of the agent is running: the hand back waits for it.
	api.activeSessionsMux.Lock()
	if api.activeSessions == nil {
		api.activeSessions = map[string]*ActiveSessionInfo{}
	}
	api.activeSessions["srv-1"] = &ActiveSessionInfo{SessionID: "srv-1", Status: "running"}
	api.activeSessionsMux.Unlock()
	if reply, err := call(owner, "owner-pair")("handback_pull", nil); err != nil || reply == nil || !reply.Busy {
		t.Fatalf("a running server turn must hold the hand back: %+v %v", reply, err)
	}
	api.activeSessionsMux.Lock()
	delete(api.activeSessions, "srv-1")
	api.activeSessionsMux.Unlock()

	// A teammate's ask was routed to the server copy but its turn is not running or queued yet: the flip must wait for
	// it, or the turn would finish only on the server. Once the turn is admitted, the flip may go ahead.
	crewStandbyImportForTest = func(_, _, _, _ string) error { return nil }
	crewStandbyRunForTest = func(context.Context, string, crewChatCall) crewChatResult {
		return crewChatResult{Status: http.StatusOK}
	}
	t.Cleanup(func() { crewStandbyImportForTest, crewStandbyRunForTest = nil, nil })
	if !api.crewStandbyRoute(t.Context(), "owner", "team", tab, "other", true) {
		t.Fatal("while the server is the writer, a teammate's ask must be routed to the server copy")
	}
	if reply, err := call(owner, "owner-pair")("handback_done", nil); err != nil || reply == nil || !reply.Busy || writer() != "server" {
		t.Fatalf("a routed call still in flight must hold the flip: %+v %v writer=%q", reply, err, writer())
	}
	api.standbyAgentCall(t.Context(), "owner", "team", crewChatCall{Operation: "agent_ask", Tab: tab, Args: json.RawMessage(`{"sender_id":"other"}`)}, false)
	if crewStandbyInFlight("team", tab) {
		t.Fatal("an admitted turn must no longer count as in flight")
	}

	// The server only continued the Mac's conversation: it is imported with the same native session id, the thread that
	// started meanwhile is taken too, and the agent is the Mac's again.
	done, diverged, err := handBack()
	if err != nil || !done || diverged != 0 || writer() != "" {
		t.Fatalf("hand back: done=%t diverged=%d err=%v writer=%q", done, diverged, err, writer())
	}
	if api.crewStandbyRoute(t.Context(), "owner", "team", tab, "other", true) || crewStandbyInFlight("team", tab) {
		t.Fatal("after the hand back, with the Mac online, an ask must go to the Mac")
	}
	if len(imported) != 2 || imported[0] != macKey+"/"+native || imported[1] != macKey+"/"+native {
		t.Fatalf("the server's conversation must be imported with its native session id (again after the thread started): %v", imported)
	}

	// Both changed: the Mac ran turns while it was offline. Both versions are kept, nothing is imported, the owner is told.
	serverWriter()
	imported = []string{"seen"}
	macJournal = first + `{"sessionId":"` + native + `","n":"mac"}` + "\n"
	done, diverged, err = handBack()
	if err != nil || !done || diverged != 1 || writer() != "" || len(imported) != 1 {
		t.Fatalf("diverged hand back: done=%t diverged=%d err=%v writer=%q imported=%v", done, diverged, err, writer(), imported)
	}
	kept, _ := filepath.Glob(filepath.Join(crewHandbackKeptDir(macRoots, tab), "*", "native", "session.jsonl"))
	if len(kept) != 1 {
		t.Fatalf("the server's version must be saved aside on the Mac: %v", kept)
	}
	if got, err := os.ReadFile(kept[0]); err != nil || string(got) != serverJournal {
		t.Fatalf("the kept copy is not the server's version: %q %v", got, err)
	}
	rows, _ := readCrewStandby("team")
	if rows[tab].KeptAt == "" || rows[tab].Sessions[macKey].Imported != 0 || crewHandbackKeptNote("team", tab) == "" {
		t.Fatalf("the owner must be told both versions were kept: %+v", rows[tab])
	}
	doc, _ := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath("owner"))
	if current := doc.Entries[productConversationRegistryEntryKey(crewProfileID, serverKey)]; current.SessionID == "srv-1" || current.SessionID == "" {
		t.Fatalf("the server's chat must start from the Mac's next copy, its own version kept as an earlier one: %+v", current)
	}
}

// PLAT-856-551: a native session moves as chunks, like the Team folder (owner, 6 Nov: "use what folder sync uses"): a
// Mac's push lands whole on the server, a push of the grown journal sends only its new chunks, and a pull brings the
// server's newer copy back to the Mac.
func TestCrewNativeSessionMovesAsChunks(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	api := f.api
	binding := crewroot.SyncBinding{Mode: "relay", PairID: "owner-pair", Side: "server", Editing: "local", PeerProjectID: "mac-crew"}
	if err := crewroot.WriteSync("team", binding); err != nil {
		t.Fatalf("pair fixture: %v", err)
	}
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	server, err := api.crewSyncRoots(context.WithValue(t.Context(), UserContextKey, owner), owner, "team")
	if err != nil {
		t.Fatalf("owner Crew roots: %v", err)
	}
	mac := crewroot.Roots{ID: "mac-crew", Owner: "owner", Private: t.TempDir()}
	router := mux.NewRouter()
	router.HandleFunc("/api/crew-sync/{project}/native/{op:plan|commit|pull|chunks}", api.handleCrewNativeSync).Methods("POST", "PUT")
	var sent int
	counting := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			body, _ := io.ReadAll(r.Body)
			sent += len(body)
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		router.ServeHTTP(w, r)
	})
	ctx := context.WithValue(t.Context(), crewTeamTransportKey{}, crewTeamInProcess{handler: counting, claims: owner})
	connection := desktopConnection{Server: "https://server.example.com"}
	macBinding := &crewroot.SyncBinding{PeerProjectID: "team", PairID: "owner-pair"}

	const native = "0f8fad5b-d9cb-469f-a165-70867728950e"
	var journal strings.Builder
	line := func(n int) {
		sum := sha256.Sum256([]byte{byte(n), byte(n >> 8)})
		journal.WriteString(`{"sessionId":"` + native + `","n":` + strconv.Itoa(n) + `,"text":"` + strings.Repeat(hex.EncodeToString(sum[:]), 8) + `"}` + "\n")
	}
	for n := 0; n < 1500; n++ {
		line(n)
	}
	bundle := func(root, text string) *sessiontransfer.Bundle {
		t.Helper()
		sum := sha256.Sum256([]byte(text))
		b := &sessiontransfer.Bundle{Provider: "claude-code", Format: "claude-jsonl", Transport: "structured", SessionID: native, Workspace: "/work/crew",
			Files: map[string]sessiontransfer.File{"session.jsonl": {Size: int64(len(text)), SHA256: hex.EncodeToString(sum[:])}}}
		stage, err := crewNativeStage(crewroot.Roots{Private: root}, b)
		if err == nil && text != "" {
			err = os.WriteFile(filepath.Join(stage, "session.jsonl"), []byte(text), 0o600)
		}
		if err != nil {
			t.Fatalf("native stage: %v", err)
		}
		b.Root = stage
		return b
	}
	serverJournal := func() string {
		data, _ := os.ReadFile(filepath.Join(server.Private, "native-sync", "claude-code-"+native, "journals", "session.jsonl"))
		return string(data)
	}

	first := journal.String()
	if err := desktopCrewNativeSync(ctx, mac, connection, macBinding, bundle(mac.Private, first)); err != nil || serverJournal() != first {
		t.Fatalf("first push did not land whole: %v", err)
	}
	if sent < len(first) {
		t.Fatalf("the first push sent %d bytes of a %d-byte journal", sent, len(first))
	}
	line(1500)
	grown := journal.String()
	sent = 0
	if err := desktopCrewNativeSync(ctx, mac, connection, macBinding, bundle(mac.Private, grown)); err != nil || serverJournal() != grown {
		t.Fatalf("second push did not land whole: %v", err)
	}
	if sent == 0 || sent > 2*chunkMaxBytes {
		t.Fatalf("a grown journal must send only its new chunks: sent %d of %d bytes", sent, len(grown))
	}

	// The server answered a turn: its copy is newer. The Mac pulls it back.
	line(1501)
	newer := journal.String()
	served := bundle(server.Private, newer)
	if err := desktopCrewNativePull(ctx, mac, connection, macBinding, &sessiontransfer.Bundle{Provider: served.Provider, Format: served.Format, Transport: served.Transport, SessionID: native, Workspace: served.Workspace, Files: served.Files}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(mac.Private, "native-sync", "claude-code-"+native, "journals", "session.jsonl")); string(data) != newer {
		t.Fatal("the pull did not bring the server's newer copy to the Mac")
	}
}

// PLAT-856: with the Mac off, a chat read can show an always-on copy, but only of an agent's own conversation and only
// for the Crew's owner (a member watching reads as the owner): a teammate's private thread with the agent is never
// served, and a member's read of their own Mac never reaches the owner's copies.
func TestCrewMacOffReadShowsOnlyTheAgentsOwnCopy(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	owner := ownerOfCrewProjectID("team")
	if owner == "" {
		t.Fatal("fixture Crew has no owner")
	}
	dir, err := crewStandbyDir("team")
	if err != nil {
		t.Fatal(err)
	}
	write := func(sub, text string) {
		t.Helper()
		raw, _ := json.Marshal(crewHandoffConversation{ConversationID: "conversation-x", History: []llmtypes.MessageContent{llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, text)}})
		if err := os.MkdirAll(filepath.Join(dir, "helper01", sub), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "helper01", sub, "conversation.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("own", "the agent's own chat")
	write("thread", "a teammate's private thread")
	if err := writeCrewStandby("team", map[string]crewAgentStandby{"helper01": {Tab: "helper01", Sessions: map[string]crewAgentStandbySession{
		"mac-crew:chat:helper01":                   {Dir: "own", ConversationID: "conversation-own"},
		"mac-crew:chat:with-helper01-0123456789ab": {Dir: "thread", ConversationID: "conversation-thread"},
	}}}); err != nil {
		t.Fatal(err)
	}
	read := func(reader, conversation string) string {
		body := f.api.crewMacOffRead(reader, "team", conversation, "events").Body
		return string(body)
	}
	if got := read(owner, "conversation-own"); !strings.Contains(got, "own chat") || !strings.Contains(got, `"history_on_mac":false`) {
		t.Fatalf("the owner (or a member watching as the owner) must see the agent's own copy: %s", got)
	}
	if got := read(owner, "conversation-thread"); strings.Contains(got, "private thread") {
		t.Fatalf("a teammate's private thread was served from the copy: %s", got)
	}
	if got := read("member", "conversation-own"); strings.Contains(got, "own chat") {
		t.Fatalf("a member's own read reached the owner's copy: %s", got)
	}
}

// PLAT-856 Always on, live test 6 Nov (capture_error "this agent has no portable native session"): an agent the server
// ran from saved history (it lacked the Mac's program) comes back to the Mac as history. The Mac takes it when its own
// history is the start of the server's, changes nothing when they are the same, and keeps the server's version aside
// when both changed.
func TestCrewHandbackFromHistoryTakesExtendsKeepsBoth(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	api := f.api
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	roots, err := api.crewSyncRoots(context.WithValue(t.Context(), UserContextKey, owner), owner, "team")
	if err != nil {
		t.Fatalf("owner Crew roots: %v", err)
	}
	roots.ID = "mac-crew"
	const tab, key = "helper01", "mac-crew:chat:helper01"
	say := func(texts ...string) []llmtypes.MessageContent {
		out := []llmtypes.MessageContent{}
		for i, text := range texts {
			role := llmtypes.ChatMessageTypeHuman
			if i%2 == 1 {
				role = llmtypes.ChatMessageTypeAI
			}
			out = append(out, llmtypes.TextParts(role, text))
		}
		return out
	}
	var macHistory []llmtypes.MessageContent
	saved := crewHandbackMacHistory
	crewHandbackMacHistory = func(string, ProductConversationRecord) ([]llmtypes.MessageContent, bool, error) {
		return macHistory, macHistory != nil, nil
	}
	crewHandbackMineForTest = func(_ crewroot.Roots, k string) (*ProductConversationRecord, *sessiontransfer.Bundle) {
		if k != key {
			return nil, nil
		}
		return &ProductConversationRecord{ConversationKey: k, SessionID: "mac-1", Provider: "claude-code"}, nil
	}
	var imported []int
	crewHandbackImportForTest = func(_ crewroot.Roots, k string, c *crewHandoffConversation) error {
		if c.Native != nil {
			t.Fatal("a history hand back must not bring a native session")
		}
		imported = append(imported, len(c.History))
		return nil
	}
	t.Cleanup(func() {
		crewHandbackMineForTest, crewHandbackImportForTest = nil, nil
		crewHandbackMacHistory = saved
	})
	server := say("remember pineapple", "ok", "also mango (asked on the website)", "ok, pineapple and mango")
	apply := func() string {
		t.Helper()
		item := crewStandbySendItem{Key: key, Conversation: &crewHandoffConversation{ConversationID: "conversation-helper", History: server}}
		event, _, err := api.applyCrewHandback(t.Context(), roots, tab, "owner-pair", item, func(*sessiontransfer.Bundle) error {
			t.Fatal("a history hand back must not pull a native session")
			return nil
		})
		if err != nil {
			t.Fatalf("hand back: %v", err)
		}
		return event
	}

	macHistory = server[:2] // the server only added the website turns
	if event := apply(); event != "imported" || len(imported) != 1 || imported[0] != len(server) {
		t.Fatalf("the Mac must take the server's longer history: event=%s imported=%v", event, imported)
	}
	macHistory = server // nothing was answered on the server
	if event := apply(); event != "unchanged" || len(imported) != 1 {
		t.Fatalf("the same history must change nothing: event=%s imported=%v", event, imported)
	}
	// The server saved only its latest messages (live test: it trims the oldest when it saves a turn): the Mac keeps
	// its full history and adds what came after its own last message.
	macHistory = say("an old question", "an old answer", "remember pineapple", "ok")
	server = say("remember pineapple", "ok", "also mango (asked on the website)", "ok, pineapple and mango")
	if event := apply(); event != "imported" || len(imported) != 2 || imported[1] != 6 {
		t.Fatalf("a trimmed server history must add only its new turns to the Mac's full history: event=%s imported=%v", event, imported)
	}
	// The Mac's last message ("ok") also ends a later server turn: the earlier place where the history matches counts.
	macHistory = say("remember pineapple", "ok")
	server = say("remember pineapple", "ok", "and mango?", "ok")
	if event := apply(); event != "imported" || len(imported) != 3 || imported[2] != 4 {
		t.Fatalf("a repeated last message must not make a false divergence: event=%s imported=%v", event, imported)
	}
	// Live round 3, 6 Nov: the server's saved history starts with its own freshly built system message, which differs
	// from the Mac's old one; that must not count as both sides having changed.
	withSystem := func(prompt string, rest ...llmtypes.MessageContent) []llmtypes.MessageContent {
		return append([]llmtypes.MessageContent{llmtypes.TextParts(llmtypes.ChatMessageTypeSystem, prompt)}, rest...)
	}
	macHistory = withSystem("old prompt", say("remember pineapple", "ok")...)
	server = withSystem("new prompt", say("remember pineapple", "ok", "and mango?", "ok mango")...)
	if event := apply(); event != "imported" || len(imported) != 4 || imported[3] != 5 {
		t.Fatalf("a different system message must not make a false divergence: event=%s imported=%v", event, imported)
	}
	macHistory = say("remember pineapple", "ok", "something only the Mac saw", "noted on the Mac") // both changed
	if event := apply(); event != "diverged" || len(imported) != 4 {
		t.Fatalf("when both changed the Mac keeps its own and the server's is kept aside: event=%s imported=%v", event, imported)
	}
}

// PLAT-856 live round 3, 6 Nov: an owner's agent answering from the server worked in an empty folder (the 15,108 team
// files were only in a member's copy). The owner of a Mac Crew now has a copy of the team folder here while an agent of
// theirs has a standby copy; it is attached to their server turns and the server note says what is really in it.
func TestOwnerTeamCopyIsAttachedToServerTurnsAndNamedInTheNote(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	_ = f
	if err := crewroot.WriteSync("team", crewroot.SyncBinding{Mode: "relay", PairID: "owner-pair", Side: "server", Editing: "local", PeerProjectID: "mac-crew"}); err != nil {
		t.Fatalf("pair fixture: %v", err)
	}
	team := crewTeam{ProjectID: "team", OwnerID: "owner", Name: "Example Crew", On: true, Folder: crewTeamFolder{ID: "folder-1", Whole: true}}
	if err := writeCrewTeam(team); err != nil {
		t.Fatalf("team fixture: %v", err)
	}
	root := filepath.Join(t.TempDir(), "team-copies", "owner")
	if err := os.MkdirAll(filepath.Join(root, "reports"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("shared"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir, err := crewTeamServerCopyDir("owner", "team", "folder-1")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = saveCrewTeamLocal(dir, crewTeamLocal{FolderID: "folder-1", Project: "team", RemoteUser: "owner", Root: root, Downloaded: true}); err != nil {
		t.Fatal(err)
	}
	// No agent of the owner has a copy here yet: nothing is attached.
	if crewOwnerCopyWanted(team) || len(crewTeamServerFolders(&UserClaims{UserID: "owner"})) != 0 {
		t.Fatal("the owner's team copy was attached before any agent could answer from the server")
	}
	if err = writeCrewStandby("team", map[string]crewAgentStandby{"main": {Tab: "main", Location: "standby", PairID: "owner-pair", Revision: 1}}); err != nil {
		t.Fatal(err)
	}
	if !crewOwnerCopyWanted(team) {
		t.Fatal("an owner with a standby copy must have a team copy")
	}
	grants := crewTeamServerFolders(&UserClaims{UserID: "owner"})
	if len(grants) != 1 || grants[0].Path != root {
		t.Fatalf("the owner's team copy must be attached to their server turns: %+v", grants)
	}
	if grants = crewTeamServerFolders(&UserClaims{UserID: "other"}); len(grants) != 0 {
		t.Fatalf("someone else got the owner's team copy: %+v", grants)
	}
	if top := crewOwnerCopyTop(team); !strings.Contains(top, "reports/") || !strings.Contains(top, "notes.md") {
		t.Fatalf("the note must name what is really in the copy: %q", top)
	}
	if note := crewStandbyServerNote("team", "main"); !strings.Contains(note, "WORK_FOLDER_EXAMPLE_CREW") || !strings.Contains(note, "reports/") {
		t.Fatalf("the server note must point at the attached copy: %s", note)
	}
}

// Owner, 6 Nov: "when their agent is online/offline/working/loading I want to see that". The Team tab says an agent is
// working while a turn is going in it; for a Mac agent that is read from the events its Mac relays, ended by the
// turn's completion, and a turn whose end never came stops counting.
func TestCrewRelayedEventsSayWhetherAMacAgentIsWorking(t *testing.T) {
	batch := func(types ...string) json.RawMessage {
		events := []map[string]string{}
		for _, ty := range types {
			events = append(events, map[string]string{"type": ty})
		}
		raw, _ := json.Marshal(map[string]any{"events": events})
		return raw
	}
	if running, known := crewRelayBatchRunning(batch("user_message", "tool_call_start")); !running || !known {
		t.Fatalf("a started turn must read as working: %t %t", running, known)
	}
	if running, known := crewRelayBatchRunning(batch("user_message", "unified_completion")); running || !known {
		t.Fatalf("a finished turn must not read as working: %t %t", running, known)
	}
	if _, known := crewRelayBatchRunning(batch("tool_call_start", "tool_call_end")); known {
		t.Fatal("events with no turn start or end say nothing about the turn")
	}
	key := "owner/team/conversation-x"
	crewRelayStreams.Lock()
	crewRelayStreams.streams[key] = &crewRelayStream{running: true, since: time.Now()}
	crewRelayStreams.Unlock()
	t.Cleanup(func() { crewRelayStreams.Lock(); delete(crewRelayStreams.streams, key); crewRelayStreams.Unlock() })
	if !crewMacConversationRunning("owner", "team", "conversation-x") {
		t.Fatal("a turn that started now must read as working")
	}
	crewRelayStreams.Lock()
	crewRelayStreams.streams[key].since = time.Now().Add(-2 * crewActivityStale)
	crewRelayStreams.Unlock()
	if crewMacConversationRunning("owner", "team", "conversation-x") {
		t.Fatal("a turn whose end never came must stop counting as working")
	}
}

// PLAT-856 round 4, 6 Nov: taking ChatGPT's Muse session back failed ("lstat …/journals/subagent/<id>: no such file")
// because the Mac had none of a sub-session's folder yet; reading what the destination already holds must skip it.
func TestChunkFilesSkipsAFileWhoseFolderIsMissing(t *testing.T) {
	dir := t.TempDir()
	files, _, err := crewNativeFiles(dir, &sessiontransfer.Bundle{Files: map[string]sessiontransfer.File{"subagent/abc/session.jsonl": {}}}, true, nil)
	if err != nil || len(files) != 0 {
		t.Fatalf("a file in a missing folder is skipped: files=%v err=%v", files, err)
	}
}
