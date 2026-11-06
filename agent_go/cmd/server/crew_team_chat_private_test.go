package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-856-509, access rule: with "Teammates can read this chat" off, the server refuses every read of the agent's chat
// by anyone but its own person, on the relay and with the Mac off, and for a chat kept on the server. The agent stays
// listed and askable, unset means visible, and nobody else can change it.
func TestCrewTeamChatPrivateRefusesTeammatesNotItsPerson(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
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
	const conversation, serverKey, serverSession = "private-check", "team:chat:agent1", "other-session"
	crewMacTeamTabsCache.Store("team\x00owner", crewMacTeamTabsRead{at: time.Now(), tabs: []crewMacTeamTab{{Person: "owner", Tab: macMainTab, ConversationID: conversation}}})
	t.Cleanup(func() { crewMacTeamTabsCache.Delete("team\x00owner") })
	crewRelayStreams.append("owner", "team", conversation, crewRelayBatch{Kind: "event", Seq: 1, Body: json.RawMessage(`{"events":[{"content":"owner-chat-text"}]}`)})
	doc := productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, serverKey): {ProfileID: crewProfileID, ConversationKey: serverKey, SessionID: serverSession}}}
	if err := defaultProductConversationRegistryStore().writeDocument(t.Context(), productConversationRegistryPath("other"), doc); err != nil {
		t.Fatal(err)
	}
	f.api.activeSessions = map[string]*ActiveSessionInfo{serverSession: {UserID: "other", Status: "completed"}}

	handler := f.api.crewChatRelayMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("server-chat-text"))
	}))
	get := func(who, session, suffix string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/sessions/"+session+"/"+suffix, nil)
		r = mux.SetURLVars(r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: who, Username: who})), map[string]string{"session_id": session})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	macSession := crewChatSessionID("team", conversation)
	refused := func(w *httptest.ResponseRecorder, secret string) bool {
		return w.Code == 403 && strings.Contains(w.Body.String(), "chat_private") && strings.Contains(w.Body.String(), "keeps this chat private") && !strings.Contains(w.Body.String(), secret)
	}

	// Unset is visible: the member reads the owner's Mac agent (Mac off: the relay's copy), the owner the member's server chat.
	if w := get("other", macSession, "events"); w.Code != 200 || !strings.Contains(w.Body.String(), "owner-chat-text") {
		t.Fatalf("unset must stay visible on the Mac-off read: %d %s", w.Code, w.Body.String())
	}
	if w := get("owner", serverSession, "events"); w.Code != 200 {
		t.Fatalf("unset must stay visible on a server chat: %d %s", w.Code, w.Body.String())
	}
	// An open watch from before the change (crewMacTeamWatchAllowed is asked again on each event of a stream).
	open := httptest.NewRequest("GET", "/api/sessions/"+macSession+"/events/stream", nil)
	open = open.WithContext(context.WithValue(open.Context(), UserContextKey, &UserClaims{UserID: "other", Username: "other"}))
	watch, ok := f.api.crewMacTeamWatchContext(open, "team", conversation)
	if !ok || !crewMacTeamWatchAllowed(watch, "team") {
		t.Fatal("the member's watch was not admitted while the chat is visible")
	}

	off, on := false, true
	// Only the agent's own person changes it.
	if _, err := setCrewTeamAgentRule(t.Context(), "team", "other", crewMacTabRow("team", crewMacTeamTab{Person: "owner", Tab: macMainTab}).ConversationKey, nil, nil, &off); !errors.Is(err, errCrewTeamAgentNotYours) {
		t.Fatalf("a member changed the owner's agent: %v", err)
	}
	if _, err := setCrewTeamAgentRule(t.Context(), "team", "owner", serverKey, nil, nil, &off); !errors.Is(err, errCrewTeamAgentNotYours) {
		t.Fatalf("the Crew's owner changed a member's agent: %v", err)
	}
	if w := get("other", macSession, "events"); w.Code != 200 {
		t.Fatalf("a refused change still took effect: %d", w.Code)
	}
	for who, key := range map[string]string{"owner": macMainTab, "other": serverKey} {
		if _, err := setCrewTeamAgentRule(t.Context(), "team", who, key, nil, nil, &off); err != nil {
			t.Fatalf("%s could not keep their own chat private: %v", who, err)
		}
	}

	// Off: every relay read, including the Mac-off read, is refused to the teammate; the open watch ends.
	for _, suffix := range []string{"events", "status", "events/stream", "queued-messages"} {
		if w := get("other", macSession, suffix); !refused(w, "owner-chat-text") {
			t.Fatalf("%s reached the teammate: %d %s", suffix, w.Code, w.Body.String())
		}
	}
	if crewMacTeamWatchAllowed(watch, "team") {
		t.Fatal("an open watch went on after the chat turned private")
	}
	// A chat kept on the server: every session route and the saved history, for anyone but its person.
	if w := get("owner", serverSession, "events"); !refused(w, "server-chat-text") {
		t.Fatalf("a member's private server chat reached the owner: %d %s", w.Code, w.Body.String())
	}
	if w := get("other", serverSession, "events"); w.Code != 200 {
		t.Fatalf("its person lost their own chat: %d %s", w.Code, w.Body.String())
	}
	view := func(who string) *http.Request {
		r := httptest.NewRequest("GET", "/api/chat-history/sessions/"+serverSession, nil)
		return mux.SetURLVars(r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: who, Username: who})), map[string]string{"session_id": serverSession})
	}
	if f.api.crewSessionTabView(view("owner"), serverSession) {
		t.Fatal("a private chat still counts as a readable tab")
	}
	history := httptest.NewRecorder()
	getChatHistoryConversationHandler(f.api)(history, view("owner"))
	if !refused(history, "server-chat-text") {
		t.Fatalf("saved history of a private chat: %d %s", history.Code, history.Body.String())
	}

	// Still listed with the server's line, and still askable.
	saved, err := readCrewTeam("team")
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, row := range crewTeamTabFor(t.Context(), saved, "other").Agents {
		if row.OwnerID == "owner" && row.Tab == macMainTab {
			listed = row.ChatPrivateLine != "" && !row.ChatVisible && row.InTeam && row.WatchKey == crewMacTabRow("team", crewMacTeamTab{Person: "owner", Tab: macMainTab}).ConversationKey
		}
	}
	if !listed {
		t.Fatal("the private agent left the teammate's list, or has no line")
	}
	if _, reason := macSharedAgentFor("team", macMainTab, "other", "owner"); reason != "" {
		t.Fatalf("a private chat's agent can no longer be asked: %s", reason)
	}

	// On again: the record holds nothing, and the teammate reads.
	if _, err := setCrewTeamAgentRule(t.Context(), "team", "owner", macMainTab, nil, nil, &on); err != nil {
		t.Fatal(err)
	}
	if saved, _ := readCrewTeam("team"); len(saved.AgentRules["owner"]) != 0 {
		t.Fatalf("on must store nothing: %+v", saved.AgentRules["owner"])
	}
	if w := get("other", macSession, "events"); w.Code != 200 || !strings.Contains(w.Body.String(), "owner-chat-text") {
		t.Fatalf("on again must be readable: %d %s", w.Code, w.Body.String())
	}
}
