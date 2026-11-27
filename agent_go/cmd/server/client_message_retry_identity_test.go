package server

import (
	"context"
	"os"
	"strings"
	"testing"
)

// #3795: a send retried after 409 turn_running carries a fresh
// Idempotency-Key but keeps its first key as the message identity, so the
// stored row replaces the bubble the browser already shows.
func TestRetriedSendKeepsItsFirstClientMessageID(t *testing.T) {
	retried := context.WithValue(context.Background(), chatSubmissionContextKey{}, chatSubmissionContext{ID: "fresh-key", ClientMessageID: "first-key"})
	if got := clientMessageIDFromContext(retried); got != "first-key" {
		t.Fatalf("retried send id = %q, want first-key", got)
	}
	plain := context.WithValue(context.Background(), chatSubmissionContextKey{}, chatSubmissionContext{ID: "only-key"})
	if got := clientMessageIDFromContext(plain); got != "only-key" {
		t.Fatalf("plain send id = %q, want only-key", got)
	}
	if sanitizeClientMessageID("bad key!") != "" {
		t.Fatal("an unsafe header value must not become a message id")
	}
}

// #3795: two sends held by one compaction used to register their client id
// before taking the input lane; the later overwrote the earlier. The query
// handler must register only after it holds the lane.
func TestClientMessageExpectationIsRegisteredAfterTheInputLane(t *testing.T) {
	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	lane := strings.Index(text, "releaseInputLane, acquired = api.tryLockSessionInputLane(sessionID, laneWait)")
	expect := strings.Index(text, "api.eventStore.ExpectClientUserMessage(sessionID, clientMessageID, req.Query)")
	if lane < 0 || expect < 0 {
		t.Fatalf("markers missing: lane=%d expect=%d", lane, expect)
	}
	if expect < lane {
		t.Fatal("ExpectClientUserMessage runs before the input lane is held")
	}
}
