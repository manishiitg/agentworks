package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-856-509, access rule: "Teammates see" is the agent's own person's choice. Just the agent: the server refuses every
// read of the chat by anyone else, on the relay and with the Mac off, and for a chat kept on the server. Chat history:
// finished turns are readable and nothing of a running turn, on every read and on the stream. The agent stays listed and
// askable, unset is Live chat, and nobody else can change it.
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
	crewRelayStreams.append("owner", "team", conversation, crewRelayBatch{Kind: "event", Seq: 4, Body: json.RawMessage(`{"events":[{"type":"user_message","sequence":2,"id":"mac-done-question"},{"type":"unified_completion","sequence":3,"id":"mac-done-answer"},{"type":"user_message","sequence":4,"id":"mac-running-question"},{"type":"streaming_chunk","id":"mac-running-partial"}]}`)})
	doc := productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, serverKey): {ProfileID: crewProfileID, ConversationKey: serverKey, SessionID: serverSession}}}
	if err := defaultProductConversationRegistryStore().writeDocument(t.Context(), productConversationRegistryPath("other"), doc); err != nil {
		t.Fatal(err)
	}
	f.api.activeSessions = map[string]*ActiveSessionInfo{serverSession: {UserID: "other", Status: "running", Query: "running-query"}}
	// The server chat's real journal: one finished turn, then a turn still running.
	journal, err := events.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := events.NewEventStore(20)
	t.Cleanup(store.Stop)
	store.SetDurableJournal(journal)
	store.SetSessionOwner(serverSession, "other")
	if err := store.SetSessionPersistenceClass(serverSession, events.SessionPersistenceInteractiveChat); err != nil {
		t.Fatal(err)
	}
	for _, kind := range [][2]string{{"done-question", "user_message"}, {"done-tool", "tool_call_start"}, {"done-answer", "unified_completion"}, {"running-question", "user_message"}, {"running-tool", "tool_call_start"}} {
		store.AddEvent(serverSession, events.Event{ID: kind[0], Type: kind[1], Timestamp: time.Now()})
	}
	f.api.eventStore, f.api.runtimeCoordinator = store, NewRuntimeCoordinator()

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
	stream := func(who, session string) string {
		r := httptest.NewRequest("GET", "/api/sessions/"+session+"/events/stream?durable_chat=1&since=0", nil)
		ctx, cancel := context.WithTimeout(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: who, Username: who}), 300*time.Millisecond)
		defer cancel()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, mux.SetURLVars(r.WithContext(ctx), map[string]string{"session_id": session}))
		return w.Body.String()
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

	off, historyOnly, on, unknown := crewChatShareAgent, crewChatShareHistory, crewChatShareLive, "everything"
	if _, err := setCrewTeamAgentRule(t.Context(), "team", "owner", macMainTab, nil, nil, &unknown); !errors.Is(err, errCrewChatShareUnknown) {
		t.Fatalf("an unknown choice was saved: %v", err)
	}
	// Chat history. Set by each agent's own person.
	for who, key := range map[string]string{"owner": macMainTab, "other": serverKey} {
		if _, err := setCrewTeamAgentRule(t.Context(), "team", who, key, nil, nil, &historyOnly); err != nil {
			t.Fatalf("%s could not choose Chat history: %v", who, err)
		}
	}
	if crewMacTeamWatchAllowed(watch, "team") {
		t.Fatal("an open live watch went on after the chat became history only")
	}
	finished := func(label, body string, want, hidden []string) {
		t.Helper()
		for _, text := range want {
			if !strings.Contains(body, text) {
				t.Fatalf("%s lost %s: %s", label, text, body)
			}
		}
		for _, text := range hidden {
			if strings.Contains(body, text) {
				t.Fatalf("%s showed the running turn's %s: %s", label, text, body)
			}
		}
	}
	// A Mac agent, Mac off: the relay's copy holds the running turn.
	macDone, macRunning := []string{"mac-done-question", "mac-done-answer"}, []string{"mac-running-question", "mac-running-partial"}
	if w := get("other", macSession, "events?durable_chat=1&limit=50"); w.Code != 200 {
		t.Fatalf("Mac history read: %d %s", w.Code, w.Body.String())
	} else {
		finished("Mac history", w.Body.String(), macDone, macRunning)
	}
	finished("Mac stream", stream("other", macSession), macDone, macRunning)
	if w := get("other", macSession, "queued-messages"); w.Code != 403 || !strings.Contains(w.Body.String(), "chat_live_off") {
		t.Fatalf("the Mac queue reached a history teammate: %d %s", w.Code, w.Body.String())
	}
	// A chat on this server: its real journal, every way of reading the tail, and a page past the running turn's start.
	serverDone, serverRunning := []string{"done-question", "done-answer"}, []string{"running-question", "running-tool"}
	cursor := int64(0)
	for _, query := range []string{"events?durable_chat=1&limit=50", "events?durable_chat=1&limit=50&view=messages", "events?limit=50", "events?durable_chat=1&limit=50&before_sequence=999", "events?durable_chat=1&since=0"} {
		w := get("owner", serverSession, query)
		if w.Code != 200 {
			t.Fatalf("history read %s: %d %s", query, w.Code, w.Body.String())
		}
		finished(query, w.Body.String(), serverDone, serverRunning)
		var page GetEventsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Events) == 0 || page.RuntimeState != nil {
			t.Fatalf("history read %s: %+v %v", query, page, err)
		}
		cursor = page.Events[len(page.Events)-1].Sequence
		if page.Events[len(page.Events)-1].ID != "done-answer" || int64(page.LastProcessedIndex) != cursor {
			t.Fatalf("history read %s: the cursor is past the finished turn: %+v", query, page)
		}
	}
	if w := get("owner", serverSession, fmt.Sprintf("events?durable_chat=1&since=%d", cursor)); w.Code != 200 || strings.Contains(w.Body.String(), "running-") {
		t.Fatalf("a forward read reached the running turn: %d %s", w.Code, w.Body.String())
	}
	finished("server stream", stream("owner", serverSession), serverDone, serverRunning)
	if w := get("owner", serverSession, "status"); w.Code != 200 || strings.Contains(w.Body.String(), "running-query") || strings.Contains(w.Body.String(), "runtime_state") {
		t.Fatalf("status told a history teammate what the turn is doing: %d %s", w.Code, w.Body.String())
	}
	for _, suffix := range []string{"main-terminal", "queued-messages", "background-work", "activity-tree"} {
		if w := get("owner", serverSession, suffix); w.Code != 403 || !strings.Contains(w.Body.String(), "chat_live_off") {
			t.Fatalf("%s reached a history teammate: %d %s", suffix, w.Code, w.Body.String())
		}
	}
	if w := get("other", serverSession, "events"); w.Code != 200 || !strings.Contains(w.Body.String(), "server-chat-text") {
		t.Fatalf("Chat history limited the agent's own person: %d %s", w.Code, w.Body.String())
	}
	// The saved conversation file: readable between turns; while one runs a steering message can already be in it.
	savedRead := httptest.NewRequest("GET", "/api/chat-history/sessions/"+serverSession, nil)
	savedRead = savedRead.WithContext(context.WithValue(savedRead.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"}))
	if f.api.crewSessionChatSavedRead(savedRead, serverSession) != nil {
		t.Fatal("Chat history refused the saved conversation between turns")
	}
	f.api.setSessionBusy(serverSession, true)
	if f.api.crewSessionChatSavedRead(savedRead, serverSession) == nil {
		t.Fatal("Chat history read the saved conversation during a turn")
	}
	f.api.setSessionBusy(serverSession, false)
	saved, err := readCrewTeam("team")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range crewTeamTabFor(t.Context(), saved, "other").Agents {
		if row.OwnerID == "owner" && row.Tab == macMainTab && (row.ChatShare != crewChatShareHistory || !strings.HasSuffix(row.ChatShareNote, "shares this chat's history. Live view is off.") || row.ChatPrivateLine != "") {
			t.Fatalf("the teammate's row has no history note: %+v", row)
		}
	}
	for who, key := range map[string]string{"owner": macMainTab, "other": serverKey} {
		if _, err := setCrewTeamAgentRule(t.Context(), "team", who, key, nil, nil, &on); err != nil {
			t.Fatal(err)
		}
	}
	f.api.activeSessions[serverSession].Status = "completed"
	watch, ok = f.api.crewMacTeamWatchContext(open, "team", conversation)
	if !ok {
		t.Fatal("the member's watch was not admitted for Live chat")
	}

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
	saved, err = readCrewTeam("team")
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, row := range crewTeamTabFor(t.Context(), saved, "other").Agents {
		if row.OwnerID == "owner" && row.Tab == macMainTab {
			listed = row.ChatPrivateLine != "" && row.ChatShare == crewChatShareAgent && row.InTeam && row.WatchKey == crewMacTabRow("team", crewMacTeamTab{Person: "owner", Tab: macMainTab}).ConversationKey
		}
	}
	if !listed {
		t.Fatal("the private agent left the teammate's list, or has no line")
	}
	if _, reason := macSharedAgentFor("team", macMainTab, "other", "owner"); reason != "" {
		t.Fatalf("a private chat's agent can no longer be asked: %s", reason)
	}

	// The earlier saved off reads as Just the agent.
	var earlier crewTeamAgentRule
	if err := json.Unmarshal([]byte(`{"chat_visible":false}`), &earlier); err != nil || earlier.ChatShare != crewChatShareAgent {
		t.Fatalf("a saved off did not stay private: %+v %v", earlier, err)
	}

	// Live chat again: the record holds nothing, and the teammate reads.
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
