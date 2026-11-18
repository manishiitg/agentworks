package server

import (
	"context"
	"testing"
	"time"
)

// Startup resumed every interrupted chat in the same second: seven native CLIs
// cold-started at once and the laptop swapped. Only startupResumeLimit chats may
// start; the rest wait durably and start as slots free; a person's new message
// starts its chat at once; a second restart neither loses nor repeats a waiting one.
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
}
