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
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-856-442: the website dropped all Mac events because the store correctly
// rejects a different session owner. Project the validated main only, keeping
// foreign events out even when mixed into an otherwise valid envelope.
func TestCrewMacEventProjectionKeepsMainAndDropsForeignSessions(t *testing.T) {
	raw := []byte(`{"session_id":"local","latest_sequence":9,"events":[
{"id":"kept","session_id":"local","execution_id":"main:local","terminal_owner_id":"main:local","terminal_id":"local:main:local","data":{"session_id":"local","data":{"session_id":"local","content":"local main:local must stay in this text"}}},
{"id":"foreign","session_id":"other","data":{"content":"private"}},
{"id":"prefix","session_id":"local-other"},
{"id":"foreign-nested","session_id":"local","data":{"session_id":"other"}},
{"id":"foreign-execution","session_id":"local","execution_id":"main:local-other"}
]}`)
	got, err := projectCrewMacEvents(raw, "local", "paired")
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		SessionID string `json:"session_id"`
		Sequence  int    `json:"latest_sequence"`
		Events    []struct {
			ID         string `json:"id"`
			SessionID  string `json:"session_id"`
			TerminalID string `json:"terminal_id"`
			Owner      string `json:"terminal_owner_id"`
		} `json:"events"`
	}
	if json.Unmarshal(got, &response) != nil || response.SessionID != "paired" || response.Sequence != 9 || len(response.Events) != 1 {
		t.Fatalf("invalid projection: %s", got)
	}
	event := response.Events[0]
	if event.ID != "kept" || event.SessionID != "paired" || event.TerminalID != "paired:main:paired" || event.Owner != "main:paired" || !bytes.Contains(got, []byte("local main:local must stay in this text")) {
		t.Fatalf("routing or content changed incorrectly: %s", got)
	}
	if _, err := projectCrewMacEvents(raw, "another-conversation", "paired"); err == nil {
		t.Fatal("foreign envelope was relabeled")
	}
}

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
	t.Run("handoff completion remains admitted after editing moves to server", func(t *testing.T) {
		// PLAT-856-442: the handoff changes Editing before the Mac posts its
		// reply. Only that owner's pending handoff may complete in this state.
		result := make(chan crewChatResult, 1)
		go func() { result <- api.callCrewMac(r, project, crewChatCall{Operation: "handoff"}) }()
		poll := httptest.NewRecorder()
		api.handleCrewChatPoll(poll, get)
		var call crewChatCall
		if poll.Code != 200 || json.Unmarshal(poll.Body.Bytes(), &call) != nil || call.Operation != "handoff" {
			t.Fatalf("handoff poll: %d %s", poll.Code, poll.Body.String())
		}
		binding, e := crewroot.ReadSync(project)
		if e != nil || binding == nil {
			t.Fatal("missing paired fixture")
		}
		binding.Editing = "server"
		if e = crewroot.WriteSync(project, *binding); e != nil {
			t.Fatal(e)
		}
		defer func() {
			binding.Editing = "local"
			if e := crewroot.WriteSync(project, *binding); e != nil {
				t.Fatal(e)
			}
		}()
		body, _ := json.Marshal(crewChatResult{ID: call.ID, Status: 200, Body: json.RawMessage(`{"complete":true}`)})
		for _, tc := range []struct {
			who  *UserClaims
			id   string
			want int
		}{
			{&UserClaims{UserID: "other", Username: "other"}, call.ID, 403},
			{owner, "unrelated", 403},
			{owner, call.ID, 204},
		} {
			var reply crewChatResult
			json.Unmarshal(body, &reply)
			reply.ID = tc.id
			raw, _ := json.Marshal(reply)
			w := httptest.NewRecorder()
			api.handleCrewChatPoll(w, request(tc.who, pair, raw))
			if w.Code != tc.want {
				t.Fatalf("handoff completion: got %d want %d: %s", w.Code, tc.want, w.Body.String())
			}
		}
		if got := <-result; got.Status != 200 {
			t.Fatalf("completion lost: %d %s", got.Status, got.Body)
		}
	})
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
