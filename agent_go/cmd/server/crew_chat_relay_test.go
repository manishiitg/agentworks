package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
{"id":"per-turn","session_id":"local","execution_id":"opaque-turn","parent_execution_id":"main:local","terminal_owner_id":"main:local","terminal_id":"local:main:local"},
{"id":"unanchored-turn","session_id":"local","execution_id":"opaque-turn","parent_execution_id":"main:local"},
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
			ID          string `json:"id"`
			SessionID   string `json:"session_id"`
			ExecutionID string `json:"execution_id"`
			TerminalID  string `json:"terminal_id"`
			Owner       string `json:"terminal_owner_id"`
		} `json:"events"`
	}
	if json.Unmarshal(got, &response) != nil || response.SessionID != "paired" || response.Sequence != 9 || len(response.Events) != 2 {
		t.Fatalf("invalid projection: %s", got)
	}
	event := response.Events[0]
	if event.ID != "kept" || event.SessionID != "paired" || event.TerminalID != "paired:main:paired" || event.Owner != "main:paired" || !bytes.Contains(got, []byte("local main:local must stay in this text")) {
		t.Fatalf("routing or content changed incorrectly: %s", got)
	}
	if response.Events[1].ID != "per-turn" || response.Events[1].ExecutionID != "opaque-turn" || response.Events[1].SessionID != "paired" {
		t.Fatalf("anchored per-turn identity changed: %s", got)
	}
	if _, err := projectCrewMacEvents(raw, "another-conversation", "paired"); err == nil {
		t.Fatal("foreign envelope was relabeled")
	}
}

// PLAT-856-426: extend the real paired-Crew fixture, so refusals cannot pass
// just because the project is absent. An allowed owner roundtrip follows them.
func checkCrewChatRelayOwnerBoundary(t *testing.T, api *StreamingAPI, project, pair string) {
	t.Helper()
	request := func(claims *UserClaims, ctx context.Context) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/crew-relay/connect", nil)
		return mux.SetURLVars(r.WithContext(context.WithValue(ctx, UserContextKey, claims)), map[string]string{"project": project})
	}
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	other := &UserClaims{UserID: "other", Username: "other"}
	for _, tc := range []struct {
		name   string
		claims *UserClaims
		pair   string
	}{
		{"other valid login", other, pair},
		{"personal MCP token", crewWriter("owner"), pair},
		{"bot", &UserClaims{UserID: "owner", BotRouteGrant: "route"}, pair},
		{"wrong pair", owner, "wrong-pair"},
	} {
		if err := api.crewRelayAdmit(t.Context(), tc.claims, crewRelayCrew{Project: project, Pair: tc.pair}); err == nil {
			t.Fatalf("%s: offered another person's or a wrong Crew", tc.name)
		}
	}
	// Contract §8.5: another account's connection is never routed, and its events for my Crew are dropped.
	otherSock, err := api.crewRelayRegister(t.Context(), other, crewRelayMessage{Type: "hello", DeviceID: "mac", Crews: []crewRelayCrew{{Project: project, Pair: pair}}}, func(crewRelayMessage) error { return nil }, nil)
	if err == nil {
		defer otherSock.unregister()
		api.crewRelayHandle(t.Context(), otherSock, crewRelayMessage{Type: "event", Project: project, Conversation: "c", Seq: 5, Event: json.RawMessage(`{"events":[{}]}`)})
	}
	if replay, _, _, stop := crewRelayStreams.subscribe("owner", project, "c", 0); len(replay) != 0 {
		stop()
		t.Fatal("another account's event reached my Crew")
	} else {
		stop()
	}
	if crewMacSocketFor("owner", project, pair) != nil {
		t.Fatal("another account's connection was routed for my Crew")
	}
	commands := make(chan crewRelayMessage, 8)
	sock, err := api.crewRelayRegister(t.Context(), owner, crewRelayMessage{Type: "hello", DeviceID: "mac", Crews: []crewRelayCrew{{Project: project, Pair: pair}}}, func(m crewRelayMessage) error { commands <- m; return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	r := request(owner, ctx)
	next := func() crewRelayMessage {
		t.Helper()
		select {
		case m := <-commands:
			if m.Type != "command" || m.Call == nil || m.Project != project {
				t.Fatalf("unexpected message to the Mac: %+v", m)
			}
			return m
		case <-ctx.Done():
			t.Fatal("the Mac got no command")
		}
		return crewRelayMessage{}
	}
	result := make(chan crewChatResult, 1)
	go func() { result <- api.callCrewMac(r, project, crewChatCall{Operation: "resolve"}) }()
	cmd := next()
	api.crewRelayHandle(t.Context(), sock, crewRelayMessage{Type: "ack", Result: &crewChatResult{ID: cmd.Call.ID, Status: 200, Body: json.RawMessage(`{"conversation_id":"local-conversation","session_id":"local-session"}`)}})
	got := <-result
	if got.Status != 200 || !bytes.Contains(got.Body, []byte("local-conversation")) || !bytes.Contains(got.Body, []byte(crewChatSessionID(project, ""))) {
		t.Fatalf("owner response lost local conversation: %+v", got)
	}
	t.Run("handoff completion remains admitted after editing moves to server", func(t *testing.T) {
		// PLAT-856-442: the handoff changes Editing before the Mac replies. Only
		// that owner's pending handoff may complete in this state.
		result := make(chan crewChatResult, 1)
		go func() { result <- api.callCrewMac(r, project, crewChatCall{Operation: "handoff"}) }()
		cmd := next()
		if cmd.Call.Operation != "handoff" {
			t.Fatalf("handoff command: %+v", cmd.Call)
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
		done := json.RawMessage(`{"complete":true}`)
		if otherSock != nil {
			api.crewRelayHandle(t.Context(), otherSock, crewRelayMessage{Type: "ack", Result: &crewChatResult{ID: cmd.Call.ID, Status: 200, Body: done}})
		}
		api.crewRelayHandle(t.Context(), sock, crewRelayMessage{Type: "ack", Result: &crewChatResult{ID: "unrelated", Status: 200, Body: done}})
		select {
		case early := <-result:
			t.Fatalf("another login or an unrelated reply completed the handoff: %+v", early)
		case <-time.After(100 * time.Millisecond):
		}
		api.crewRelayHandle(t.Context(), sock, crewRelayMessage{Type: "ack", Result: &crewChatResult{ID: cmd.Call.ID, Status: 200, Body: done}})
		if got := <-result; got.Status != 200 {
			t.Fatalf("completion lost: %d %s", got.Status, got.Body)
		}
	})
	sock.unregister()
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
			if got := api.routeCrewMainChat(w, request(tc.claims, t.Context()), tc.profile, tc.key, crewChatCall{Operation: "resolve"}); got != tc.want {
				t.Fatalf("%s: routed=%v want=%v: %s", tc.name, got, tc.want, w.Body.String())
			}
			if tc.want && w.Code != 503 {
				t.Fatalf("main owner should see Mac offline, not a server chat: %d", w.Code)
			}
			// PLAT-856-509: a caller the relay does not route must not get a turn in the empty online copy either.
			if !tc.want && tc.profile == "work" && !crewLivesOnMac(tc.profile, tc.key) {
				t.Fatalf("%s: would run a server turn in a Crew that lives on the Mac", tc.name)
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

// PLAT-856-509 contract §8: the Mac's real connection code against the real
// server handler: connect, a command round trip through the local chat, a
// drop (offline refusal and the offline snapshot) and a reconnect.
func checkCrewRelaySocketEndToEnd(t *testing.T, api *StreamingAPI, project, pair string) {
	t.Helper()
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	request := func(ctx context.Context, path string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		return mux.SetURLVars(r.WithContext(context.WithValue(ctx, UserContextKey, owner)), map[string]string{"project": project})
	}
	waitFor := func(online bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if (crewMacSocketFor("owner", project, pair) != nil) == online {
				return
			}
		}
		t.Fatalf("Mac connection online=%v never happened", online)
	}
	connect := func() (context.CancelFunc, chan error) {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- api.desktopCrewRelaySession(ctx, "saved") }()
		waitFor(true)
		return cancel, done
	}
	stop, done := connect()
	resolved := api.callCrewMac(request(t.Context(), "/"), project, crewChatCall{Operation: "resolve"})
	var conversation struct {
		ConversationID string `json:"conversation_id"`
	}
	if resolved.Status != 200 || json.Unmarshal(resolved.Body, &conversation) != nil || conversation.ConversationID == "" {
		t.Fatalf("command through the Mac connection: %d %s", resolved.Status, resolved.Body)
	}
	// Load on tap: the owner reads a file of the Crew's folder; nothing outside it.
	local, err := api.crewSyncRoots(t.Context(), owner, "local-id")
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(local.Files, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(filepath.Join(local.Files, "escape.txt"))
	cardPath := "note.txt"
	if local.SeparateFiles() {
		cardPath = "_files/note.txt" // as the reply card names it
	}
	for path, want := range map[string]int{cardPath: 200, "../../outside.txt": 404, "escape.txt": 404} {
		got := api.callCrewMac(request(t.Context(), "/"), project, crewChatCall{Operation: "file", ConversationID: conversation.ConversationID, Path: path})
		if got.Status != want || want == 200 && !bytes.Contains(got.Body, []byte(base64.StdEncoding.EncodeToString([]byte("local work")))) || bytes.Contains(got.Body, []byte(base64.StdEncoding.EncodeToString([]byte("private")))) {
			t.Fatalf("file %s: %d %s", path, got.Status, got.Body)
		}
	}
	stop()
	<-done
	waitFor(false)
	if got := api.callCrewMac(request(t.Context(), "/"), project, crewChatCall{Operation: "resolve"}); got.Status != 503 {
		t.Fatalf("dropped Mac must refuse: %d", got.Status)
	}
	saveCrewMacState(project, crewMacState{State: "idle", At: time.Now().UTC().Format(time.RFC3339)})
	viewCtx, viewDone := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer viewDone()
	view := httptest.NewRecorder()
	api.serveCrewMacEvents(view, request(viewCtx, "/api/sessions/x/events/stream"), project, conversation.ConversationID)
	if !bytes.Contains(view.Body.Bytes(), []byte("event: snapshot")) || !bytes.Contains(view.Body.Bytes(), []byte(`"state":{"state":"idle"`)) {
		t.Fatalf("offline viewer did not get the saved state: %d %s", view.Code, view.Body.String())
	}
	stop, done = connect()
	defer func() { stop(); <-done }()
	if got := api.callCrewMac(request(t.Context(), "/"), project, crewChatCall{Operation: "resolve"}); got.Status != 200 {
		t.Fatalf("reconnected Mac: %d %s", got.Status, got.Body)
	}
}
