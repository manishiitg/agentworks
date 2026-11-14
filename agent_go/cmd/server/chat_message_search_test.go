package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
)

// PLAT-856: search and an old message link must use the same live admission
// as chat reads. A positive shared case proves refusals aren't missing setup.
func TestChatMessageSearchRechecksPrivateAndHiddenChats(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	const key = "team:chat:root-owner"
	f.write(t, externalCommandCrewSettings+"/product.json", `{"schema_version":1,"product":"work","id":"team","title":"Test Team","main_chat_key":"`+key+`"}`)
	record := ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: key, WorkspacePath: externalCommandCrewRoot, SessionID: "owner-main", Title: "Shared agent"}
	if err := defaultProductConversationRegistryStore().writeDocument(t.Context(), productConversationRegistryPath("owner"), productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, key): record}}); err != nil {
		t.Fatal(err)
	}
	agents := crewTeamAgentFixtures("team", map[string]map[string]bool{"owner": {macMainTab: true}, "other": {"agent1": true}})
	for id, a := range agents {
		if a.OwnerID == "owner" {
			a.ChatKey = key
			agents[id] = a
		}
	}
	if err := writeCrewTeamAgentFixture(crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}}, Agents: agents}); err != nil {
		t.Fatal(err)
	}
	journal, err := storeevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := storeevents.NewEventStore(100)
	t.Cleanup(store.Stop)
	store.SetDurableJournal(journal)
	f.api.eventStore = store
	add := func(session, owner, text string) {
		t.Helper()
		store.SetSessionOwner(session, owner)
		if err := store.SetSessionPersistenceClass(session, storeevents.SessionPersistenceInteractiveChat); err != nil {
			t.Fatal(err)
		}
		store.AddEvent(session, storeevents.Event{ID: session + "-message", Type: "user_message", Timestamp: time.Now(), Data: agentevents.NewAgentEvent(agentevents.NewUserMessageEvent(1, text, "user"))})
	}
	add("owner-main", "owner", "needle shared text")
	add("only-other", "other", "needle own text")
	add("private-stranger", "stranger", "needle stranger secret")
	call := func(handler http.HandlerFunc, user string, input any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(input)
		r := httptest.NewRequest("POST", "/api/chat-search", bytes.NewReader(raw))
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	search := func(user string) *httptest.ResponseRecorder {
		return call(f.api.handleChatMessageSearch, user, chatSearchInput{Query: "needle"})
	}
	w := search("other")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "needle shared text") || !strings.Contains(w.Body.String(), "needle own text") || strings.Contains(w.Body.String(), "stranger secret") {
		t.Fatalf("admitted search = %d %s", w.Code, w.Body.String())
	}
	input := map[string]any{"chat": "owner-main", "id": "owner-main-message", "sequence": 1}
	if w = call(f.api.handleChatMessageOpen, "other", input); w.Code != 200 || !strings.Contains(w.Body.String(), "needle shared text") {
		t.Fatalf("admitted link = %d %s", w.Code, w.Body.String())
	}
	off := crewChatShareAgent
	if _, err = setCrewTeamAgentRule(t.Context(), "team", "owner", macMainTab, nil, nil, &off); err != nil {
		t.Fatal(err)
	}
	if w = search("other"); w.Code != 200 || strings.Contains(w.Body.String(), "needle shared text") || !strings.Contains(w.Body.String(), "needle own text") {
		t.Fatalf("hidden search = %d %s", w.Code, w.Body.String())
	}
	if w = call(f.api.handleChatMessageOpen, "other", input); w.Code != 404 || strings.Contains(w.Body.String(), "needle shared text") {
		t.Fatalf("old link bypassed Hidden = %d %s", w.Code, w.Body.String())
	}
	if w = search("owner"); w.Code != 200 || !strings.Contains(w.Body.String(), "needle shared text") {
		t.Fatalf("owner lost Hidden chat = %d %s", w.Code, w.Body.String())
	}
	if w = call(f.api.handleChatMessageOpen, "other", map[string]any{"chat": "only-other", "id": "invented", "sequence": 1}); w.Code != 404 {
		t.Fatalf("forged message ID accepted = %d", w.Code)
	}

	// A full page of private rows cannot expose its boundary chat through the
	// continuation token. Replaying it as another person or tampering fails.
	for i := 0; i < 130; i++ {
		store.AddEvent("private-stranger", storeevents.Event{ID: fmt.Sprintf("private-%d", i), Type: "user_message", Timestamp: time.Now(), Data: agentevents.NewAgentEvent(agentevents.NewUserMessageEvent(1, "needle private tail", "user"))})
	}
	w = search("other")
	var first struct {
		Results []chatSearchHit `json:"results"`
		Cursor  string          `json:"next_cursor"`
	}
	if json.Unmarshal(w.Body.Bytes(), &first) != nil || w.Code != 200 || len(first.Results) != 0 || first.Cursor == "" {
		t.Fatalf("private page = %d %s", w.Code, w.Body.String())
	}
	decoded, _ := base64.StdEncoding.DecodeString(first.Cursor)
	if strings.Contains(string(decoded), "private-stranger") || strings.Contains(first.Cursor, "private-stranger") {
		t.Fatal("cursor exposed another person's chat")
	}
	w = call(f.api.handleChatMessageSearch, "other", chatSearchInput{Query: "needle", Cursor: first.Cursor})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "needle own text") || strings.Contains(w.Body.String(), "needle private tail") {
		t.Fatalf("admitted continuation = %d %s", w.Code, w.Body.String())
	}
	for _, scenario := range []struct{ user, cursor string }{{"owner", first.Cursor}, {"other", first.Cursor + "x"}} {
		if w = call(f.api.handleChatMessageSearch, scenario.user, chatSearchInput{Query: "needle", Cursor: scenario.cursor}); w.Code != 400 {
			t.Fatalf("foreign/tampered cursor accepted: %d", w.Code)
		}
	}
}
