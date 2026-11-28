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

// PLAT-856-509, access rule: "Teammates see" is the agent's own person's choice. Hidden: the server refuses every read of
// the chat by anyone else, on the relay and with the Mac off, and for a chat kept on the server. The agent stays listed
// and askable, unset is Live, and nobody else can change it.
func TestCrewTeamChatPrivateRefusesTeammatesNotItsPerson(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	const defaultKey = "team:chat:root-owner"
	f.write(t, externalCommandCrewSettings+"/product.json", `{"schema_version":1,"product":"work","id":"team","title":"Test Team","session_id":"sess-team","default_agent_key":"`+defaultKey+`"}`)
	if err := defaultProductConversationRegistryStore().writeDocument(t.Context(), productConversationRegistryPath("owner"), productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, defaultKey): {ProfileID: crewProfileID, ConversationKey: defaultKey, WorkspacePath: externalCommandCrewRoot, SessionID: "owner-main"}}}); err != nil {
		t.Fatal(err)
	}
	agents := crewTeamAgentFixtures("team", map[string]map[string]bool{"owner": {macDefaultTab: true}, "other": {"agent1": true}})
	for id, a := range agents {
		if a.OwnerID == "owner" {
			a.ChatKey = defaultKey
			agents[id] = a
		}
	}
	if err := crewroot.WriteSync("team", crewroot.SyncBinding{Mode: "relay", Side: "server", Editing: "local", PairID: "owner-pair", PeerProjectID: "mac-crew"}); err != nil {
		t.Fatal(err)
	}
	// The current registry is the permission source; native tab names alone
	// no longer create identities (registry consolidation dbcc51395c).
	if err := writeCrewTeamAgentFixture(crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}}, Agents: agents}); err != nil {
		t.Fatal(err)
	}
	if err := writeMacSharedAgents("team", []macSharedAgent{{Tab: macDefaultTab, Audience: "team"}}); err != nil {
		t.Fatal(err)
	}
	const conversation, serverKey, serverSession = "private-check", "team:chat:agent1", "other-session"
	crewMacTeamTabsCache.Store("team\x00owner", crewMacTeamTabsRead{at: time.Now(), tabs: []crewMacTeamTab{{Person: "owner", Tab: macDefaultTab, ConversationID: conversation}}})
	t.Cleanup(func() { crewMacTeamTabsCache.Delete("team\x00owner") })
	crewRelayStreams.append("owner", "team", conversation, crewRelayBatch{Kind: "event", Seq: 1, Body: json.RawMessage(`{"events":[{"content":"owner-chat-text"}]}`)})
	crewRelayStreams.append("owner", "team", conversation, crewRelayBatch{Kind: "event", Seq: 4, Body: json.RawMessage(`{"events":[{"type":"user_message","sequence":2,"id":"mac-done-question"},{"type":"unified_completion","sequence":3,"id":"mac-done-answer"},{"type":"user_message","sequence":4,"id":"mac-running-question"},{"type":"streaming_chunk","id":"mac-running-partial"}]}`)})
	doc := productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, serverKey): {ProfileID: crewProfileID, ConversationKey: serverKey, SessionID: serverSession}}}
	if err := defaultProductConversationRegistryStore().writeDocument(t.Context(), productConversationRegistryPath("other"), doc); err != nil {
		t.Fatal(err)
	}
	f.api.activeSessions = map[string]*ActiveSessionInfo{serverSession: {UserID: "other", Status: "running", Query: "running-query"}}
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
		return w.Code == 403 && strings.Contains(w.Body.String(), "chat_private") && strings.Contains(w.Body.String(), "shares this agent, not the chat") && !strings.Contains(w.Body.String(), secret)
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

	off, unknown := crewChatShareAgent, "everything"
	if _, err := setCrewTeamAgentRule(t.Context(), "team", "owner", macDefaultTab, nil, &unknown); !errors.Is(err, errCrewChatShareUnknown) {
		t.Fatalf("an unknown choice was saved: %v", err)
	}

	// Only the agent's own person changes it.
	if _, err := setCrewTeamAgentRule(t.Context(), "team", "other", crewMacTabRow("team", crewMacTeamTab{Person: "owner", Tab: macDefaultTab}).ConversationKey, nil, &off); !errors.Is(err, errCrewTeamAgentNotYours) {
		t.Fatalf("a member changed the owner's agent: %v", err)
	}
	if _, err := setCrewTeamAgentRule(t.Context(), "team", "owner", serverKey, nil, &off); !errors.Is(err, errCrewTeamAgentNotYours) {
		t.Fatalf("the Crew's owner changed a member's agent: %v", err)
	}
	if w := get("other", macSession, "events"); w.Code != 200 {
		t.Fatalf("a refused change still took effect: %d", w.Code)
	}
	// Owner, 28 Nov: sharing is one choice (agent-rule share); Agent only keeps the agent shared and its chat private.
	choose := func(who, key, share string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/crew-teams/team/agent-rule", strings.NewReader(`{"agent_key":"`+key+`","share":"`+share+`"}`))
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: who, Username: who}))
		w := httptest.NewRecorder()
		handleCrewTeamAgentRule(w, r, "team", who)
		return w
	}
	if w := choose("owner", macDefaultTab, "everything"); w.Code != 400 {
		t.Fatalf("an unknown share choice was accepted: %d", w.Code)
	}
	if w := choose("owner", macDefaultTab, "agent_only"); w.Code != 200 || !strings.Contains(w.Body.String(), `"share":"agent_only"`) || !strings.Contains(w.Body.String(), `"in_team":true`) {
		t.Fatalf("the owner could not share their agent as Agent only: %d %s", w.Code, w.Body.String())
	}
	// Saving sharing drops the Mac's tab list cache; the Mac would send it again, the fixture seeds it again.
	crewMacTeamTabsCache.Store("team\x00owner", crewMacTeamTabsRead{at: time.Now(), tabs: []crewMacTeamTab{{Person: "owner", Tab: macDefaultTab, ConversationID: conversation}}})
	if _, err := setCrewTeamAgentRule(t.Context(), "team", "other", serverKey, nil, &off); err != nil {
		t.Fatalf("other could not keep their own chat private: %v", err)
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
	// Through the /api middleware, as the server mounts it.
	f.api.crewChatRelayMiddleware(getChatHistoryConversationHandler(f.api)).ServeHTTP(history, view("owner"))
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
		if row.OwnerID == "owner" && row.Tab == macDefaultTab {
			listed = row.ChatPrivateLine != "" && row.ChatShare == crewChatShareAgent && row.InTeam && row.WatchKey == crewMacTabRow("team", crewMacTeamTab{Person: "owner", Tab: macDefaultTab}).ConversationKey
		}
	}
	if !listed {
		t.Fatal("the private agent left the teammate's list, or has no line")
	}
	if _, reason := macSharedAgentFor("team", macDefaultTab, "other", "owner"); reason != "" {
		t.Fatalf("a private chat's agent can no longer be asked: %s", reason)
	}

	// Full chat again: the record holds nothing, and the teammate reads.
	if w := choose("owner", macDefaultTab, "full_chat"); w.Code != 200 || !strings.Contains(w.Body.String(), `"share":"full_chat"`) {
		t.Fatalf("back to Full chat: %d %s", w.Code, w.Body.String())
	}
	if saved, _ := readCrewTeam("team"); !saved.agentRule("owner", macDefaultTab).empty() {
		t.Fatalf("on must store nothing: %+v", saved.Agents)
	}
	crewMacTeamTabsCache.Store("team\x00owner", crewMacTeamTabsRead{at: time.Now(), tabs: []crewMacTeamTab{{Person: "owner", Tab: macDefaultTab, ConversationID: conversation}}})
	if w := get("other", macSession, "events"); w.Code != 200 || !strings.Contains(w.Body.String(), "owner-chat-text") {
		t.Fatalf("on again must be readable: %d %s", w.Code, w.Body.String())
	}
}
