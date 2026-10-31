package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

// PLAT-856-426: extend the real paired-Crew fixture, so refusals cannot pass
// just because the project is absent. An allowed owner roundtrip follows them.
func checkCrewChatRelayOwnerBoundary(t *testing.T, api *StreamingAPI, project, pair string) {
	t.Helper()
	request := func(claims *UserClaims, suppliedPair string, body []byte) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/crew-chat/"+project+"/poll?pair="+suppliedPair, bytes.NewReader(body))
		r = r.WithContext(context.WithValue(t.Context(), UserContextKey, claims))
		return mux.SetURLVars(r, map[string]string{"project": project})
	}
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	for _, tc := range []struct {
		name   string
		claims *UserClaims
		pair   string
		want   int
	}{
		{"other valid login", &UserClaims{UserID: "other", Username: "other"}, pair, 403},
		{"personal MCP token", crewWriter("owner"), pair, 403},
		{"bot", &UserClaims{UserID: "owner", BotRouteGrant: "route"}, pair, 403},
		{"wrong pair", owner, "wrong-pair", 403},
		{"owner", owner, pair, 204},
	} {
		w := httptest.NewRecorder()
		api.handleCrewChatPoll(w, request(tc.claims, tc.pair, []byte(`{"id":"expired","status":200,"body":{}}`)))
		if w.Code != tc.want {
			t.Fatalf("%s: got %d want %d: %s", tc.name, w.Code, tc.want, w.Body.String())
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	r := mux.SetURLVars(request(owner, pair, nil).WithContext(context.WithValue(ctx, UserContextKey, owner)), map[string]string{"project": project})
	result := make(chan crewChatResult, 1)
	go func() { result <- api.callCrewMac(r, project, crewChatCall{Operation: "resolve"}) }()
	poll := httptest.NewRecorder()
	get := r.Clone(r.Context())
	get.Method = http.MethodGet
	api.handleCrewChatPoll(poll, get)
	var call crewChatCall
	if poll.Code != 200 || json.Unmarshal(poll.Body.Bytes(), &call) != nil || call.Operation != "resolve" {
		t.Fatalf("poll: %d %s", poll.Code, poll.Body.String())
	}
	reply, _ := json.Marshal(crewChatResult{ID: call.ID, Status: 200, Body: json.RawMessage(`{"conversation_id":"local-conversation","session_id":"local-session"}`)})
	w := httptest.NewRecorder()
	api.handleCrewChatPoll(w, request(owner, pair, reply))
	got := <-result
	if got.Status != 200 || !bytes.Contains(got.Body, []byte("local-conversation")) || !bytes.Contains(got.Body, []byte(crewChatSessionID(project, ""))) {
		t.Fatalf("owner response lost local conversation: %+v", got)
	}
	crewChatRelays.Lock()
	box := crewChatRelays.boxes[crewChatMailboxKey("owner", project, pair)]
	box.lastPoll = time.Time{}
	crewChatRelays.Unlock()
	if got := api.callCrewMac(r, project, crewChatCall{Operation: "send", Message: "hello"}); got.Status != 503 {
		t.Fatalf("offline must refuse, never execute on server: %d", got.Status)
	}
	t.Run("local canonical conversation rejects a stale website generation", func(t *testing.T) {
		t.Setenv("DEFAULT_USER_ID", "owner")
		resolved := api.runDesktopCrewChat(t.Context(), "local-id", pair, crewChatCall{ID: "resolve", Operation: "resolve"})
		var current AgentProfileConversationResponse
		if resolved.Status != 200 || json.Unmarshal(resolved.Body, &current) != nil || current.ConversationID == "" {
			t.Fatalf("resolve local: %d %s", resolved.Status, resolved.Body)
		}
		stale := api.runDesktopCrewChat(t.Context(), "local-id", pair, crewChatCall{ID: "stale", Operation: "cancel", ConversationID: "old-conversation"})
		if stale.Status != 409 {
			t.Fatalf("stale website must not cancel the new local conversation: %d", stale.Status)
		}
		if crewChatSessionID(project, current.ConversationID) == crewChatSessionID(project, "old-conversation") {
			t.Fatal("new chat reused old history cursor address")
		}
	})
	t.Run("main website chat routes locally but other callers do not", func(t *testing.T) {
		t.Setenv("LOCAL_MODE", "false")
		for _, tc := range []struct {
			name, profile, key string
			claims             *UserClaims
			want               bool
		}{
			{"main owner", "work", project, owner, true},
			{"side chat", "work", project + ":chat:another", owner, false},
			{"other user", "work", project, &UserClaims{UserID: "other", Username: "other"}, false},
			{"personal token", "work", project, crewWriter("owner"), false},
			{"Goal", "goal", project, owner, false},
			{"Relay", "relay", project, owner, false},
		} {
			w := httptest.NewRecorder()
			if got := api.routeCrewMainChat(w, request(tc.claims, pair, nil), tc.profile, tc.key, crewChatCall{Operation: "resolve"}); got != tc.want {
				t.Fatalf("%s: routed=%v want=%v: %s", tc.name, got, tc.want, w.Body.String())
			}
			if tc.want && w.Code != 503 {
				t.Fatalf("main owner should see Mac offline, not a server chat: %d", w.Code)
			}
		}
	})
}

// Imported project IDs need not be UUIDs. The route separator must not split
// a project or conversation containing a dot (PLAT-856-428).
func TestCrewChatVirtualSessionKeepsDottedIDsAndConversationGeneration(t *testing.T) {
	route := crewChatSessionID("custom.project", "conversation.with.dot")
	project, conversation := crewChatSessionParts(route)
	if project != "custom.project" || conversation != "conversation.with.dot" {
		t.Fatalf("route lost identity: %q %q", project, conversation)
	}
	if route == crewChatSessionID(project, "next-conversation") {
		t.Fatal("new conversation reused old event cursor address")
	}
}
