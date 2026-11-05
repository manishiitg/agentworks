package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// PLAT-856 Always on, step 1: the server keeps an In team agent's standby copy from its owner's Mac through the
// normal sync request, admitted only for the owner on the Crew's own pair. A resend of the same copy changes nothing,
// a copy that does not extend the stored one keeps both, and the owner's All my agents off drops an agent's copy
// (Main is always In team, so it stays eligible).
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
	// The owner turns All my agents off: an agent they never chose for is Only me and its copy is dropped; Main is
	// always In team and stays eligible.
	team.AgentChoices = map[string]map[string]bool{"owner": {crewTeamAllMine: false}}
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
		t.Fatalf("Main must stay eligible (always In team): %s", reason)
	}
}

// PLAT-856 Always on, step 2: with the owner's Mac on, an In team agent still answers on the Mac. With the Mac off, a
// teammate's message to it runs on the server from its standby copy, as the teammate (the asker the "When others ask"
// rule sees), and the server becomes its writer. An Only me agent is never answered from the server: it waits.
func TestCrewStandbyAnswersOnServerWhileMacOff(t *testing.T) {
	crewAlwaysOnAnswering = true // off by default until handing back (step 3) is in
	t.Cleanup(func() { crewAlwaysOnAnswering = false })
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
	if label, _ := crewAlwaysOnPlaceLabel("team", "owner", helper, true, true); label != "Mac" {
		t.Fatalf("Mac on label: %q", label)
	}

	// The Mac goes off: the teammate asks Helper from their own AI app.
	crewMacSockets.Lock()
	delete(crewMacSockets.byDevice, "owner-mac")
	crewMacSockets.Unlock()
	if label, serving := crewAlwaysOnPlaceLabel("team", "owner", helper, false, true); label != "Mac off · answering from the server" || !serving {
		t.Fatalf("In team, Mac off label: %q %t", label, serving)
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
	if label, serving := crewAlwaysOnPlaceLabel("team", "owner", solo, false, true); label != "Mac off · waits for your Mac" || serving {
		t.Fatalf("Only me, Mac off label: %q %t", label, serving)
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
