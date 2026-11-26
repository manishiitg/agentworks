package server

import (
	"context"
	"testing"
	"time"

	unifiedevents "github.com/manishiitg/mcpagent/events"
)

// Startup resumed every interrupted chat in the same second: seven native CLIs
// cold-started at once and the laptop swapped. Only startupResumeLimit chats may
// start; the rest wait durably and start as slots free; a person's new message
// starts its chat at once; a second restart neither loses nor repeats a waiting one;
// a slot frees at the resumed turn's first model event or after a short cap.
func TestStartupResumeStartsOnlyTheLimitAndAdmitsTheRestInOrder(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[]}`)
	owner := GetDefaultUserID()
	files := map[string]string{}
	seed := newConversationTurnQueueTestAPI(files)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: owner})
	sessions := []string{"chat-a", "chat-b", "chat-c", "chat-d"}
	for _, session := range sessions {
		if _, _, err := seed.enqueueConversationTurn(ctx, owner, session, QueryRequest{AgentMode: "multi-agent", Query: "continue " + session}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // distinct CreatedAt: oldest chats resume first
	}

	restart := func() (*StreamingAPI, chan string, map[string]chan struct{}) {
		api := newConversationTurnQueueTestAPI(files)
		started := make(chan string, 16)
		release := map[string]chan struct{}{}
		for _, session := range sessions {
			release[session] = make(chan struct{})
		}
		api.queuedTurnRunner = func(turn queuedConversationTurn) {
			started <- turn.SessionID
			<-release[turn.SessionID] // the turn ends, fails or is cancelled
			_ = api.removeConversationTurn(context.Background(), turn)
		}
		api.recoverConversationTurnQueue(context.Background())
		return api, started, release
	}
	next := func(started chan string) string {
		t.Helper()
		select {
		case session := <-started:
			return session
		case <-time.After(5 * time.Second):
			t.Fatal("no chat started")
			return ""
		}
	}
	quiet := func(started chan string) {
		t.Helper()
		select {
		case session := <-started:
			t.Fatalf("%s started beyond the limit", session)
		case <-time.After(200 * time.Millisecond):
		}
	}
	got := func(started chan string, n int) map[string]bool {
		t.Helper()
		out := map[string]bool{}
		for i := 0; i < n; i++ {
			session := next(started)
			if out[session] {
				t.Fatalf("%s started twice", session)
			}
			out[session] = true
		}
		return out
	}

	api, started, release := restart()
	if first := got(started, startupResumeLimit); !first["chat-a"] || !first["chat-b"] {
		t.Fatalf("oldest chats did not resume first: %v", first)
	}
	quiet(started)

	// Second restart while two chats wait: both are still queued, once each.
	api, started, release = restart()
	second := got(started, startupResumeLimit)
	quiet(started)
	// Started originals are never replayed (restart continuation owns them).
	if !second["chat-c"] || !second["chat-d"] {
		t.Fatalf("waiting chats lost or replaced after a second restart: started %v", second)
	}

	// Release and new activity, on a restart with four waiting chats.
	for _, session := range []string{"chat-e", "chat-f", "chat-g", "chat-h"} {
		sessions = append(sessions, session)
		if _, _, err := seed.enqueueConversationTurn(ctx, owner, session, QueryRequest{AgentMode: "multi-agent", Query: "continue " + session}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	api, started, release = restart()
	third := got(started, startupResumeLimit)
	quiet(started)
	if !third["chat-e"] || !third["chat-f"] {
		t.Fatalf("oldest chats did not resume first: %v", third)
	}
	api.conversationTurnQueueMu.Lock()
	deferred := append([]string(nil), api.startupResumeWaiting...)
	api.conversationTurnQueueMu.Unlock()
	if len(deferred) != 2 || deferred[0] != "chat-g" || deferred[1] != "chat-h" {
		t.Fatalf("deferred chats: %v", deferred)
	}

	// A person's message in a deferred chat starts it now, without a slot.
	person := deferred[len(deferred)-1]
	if _, _, err := api.enqueueConversationTurn(ctx, owner, person, QueryRequest{AgentMode: "multi-agent", Query: "new message"}); err != nil {
		t.Fatal(err)
	}
	api.kickConversationTurnQueue(person)
	if session := next(started); session != person {
		t.Fatalf("person's chat waited behind recovery: %s started instead", session)
	}
	quiet(started)

	// One resumed turn ends (or is cancelled): exactly the next deferred chat starts.
	close(release["chat-e"])
	if session := next(started); session != deferred[0] {
		t.Fatalf("slot went to %s, want the oldest deferred %s", session, deferred[0])
	}
	quiet(started)

	// The slot guards the CLI cold start, not the whole turn: it frees at the
	// first model event or after the cap, so a resumed turn that runs for an
	// hour does not leave the other interrupted chats idle.
	oldCap := startupResumeCap
	startupResumeCap = 2 * time.Second
	t.Cleanup(func() { startupResumeCap = oldCap })
	files = map[string]string{}
	seed = newConversationTurnQueueTestAPI(files)
	sessions = []string{"chat-i", "chat-j", "chat-k", "chat-l"}
	for _, session := range sessions {
		if _, _, err := seed.enqueueConversationTurn(ctx, owner, session, QueryRequest{AgentMode: "multi-agent", Query: "continue " + session}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	api, started, _ = restart()
	if first := got(started, startupResumeLimit); !first["chat-i"] || !first["chat-j"] {
		t.Fatalf("oldest chats did not resume first: %v", first)
	}
	api.startupResumeTurnEvent("chat-i", unifiedevents.UserMessage) // the server's own echo, before the CLI is up
	quiet(started)
	api.startupResumeTurnEvent("chat-i", unifiedevents.StreamingStart)
	if session := next(started); session != "chat-k" {
		t.Fatalf("first model event freed the slot for %s, want chat-k", session)
	}
	// chat-j never streams: its cap frees the slot for the last chat.
	if session := next(started); session != "chat-l" {
		t.Fatalf("cap freed the slot for %s, want chat-l", session)
	}
	quiet(started)
}

// A panic while dispatching a queued message stopped the whole server (from the
// dispatch goroutine) or left the chat marked as dispatching forever (from a
// request), so no later message in it ever ran. The next message must run.
func TestQueuedTurnPanicLeavesTheChatUsable(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[]}`)
	owner := GetDefaultUserID()
	api := newConversationTurnQueueTestAPI(map[string]string{})
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: owner})
	ran := make(chan string, 4)
	api.queuedTurnRunner = func(turn queuedConversationTurn) {
		ran <- turn.Request.Query
		if turn.Request.Query == "first" {
			panic("dispatch bug")
		}
		_ = api.removeConversationTurn(context.Background(), turn)
	}
	send := func(query string) {
		t.Helper()
		if _, _, err := api.enqueueConversationTurn(ctx, owner, "chat-a", QueryRequest{AgentMode: "multi-agent", Query: query}); err != nil {
			t.Fatal(err)
		}
		api.kickConversationTurnQueue("chat-a")
		select {
		case got := <-ran:
			if got != query {
				t.Fatalf("ran %q, want %q", got, query)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("queued message %q never ran", query)
		}
	}
	send("first")
	deadline := time.Now().Add(5 * time.Second)
	for api.conversationTurnDispatchPending("chat-a") {
		if time.Now().After(deadline) {
			t.Fatal("chat still marked as dispatching after the panic")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The panicked message is failed, not left started where it never runs
	// again and the queue retries around it for good.
	stored, err := api.readConversationTurnQueue(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	failed := false
	for _, row := range stored {
		if row.Request.Query == "first" {
			failed = row.StartedAt == nil && row.DeliveryFailedAt != nil
		}
	}
	if !failed {
		t.Fatal("the message whose dispatch panicked was not marked failed")
	}
	send("second")
}
