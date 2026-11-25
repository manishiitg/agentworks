package server

import (
	"strings"
	"testing"
)

// #3920: Antigravity 1.5+ takes a message into its running reply. Only a chat on an older agy says the message waits
// for the reply to end, and offers Stop; a reply that can take it, another app or a queued-on-purpose message don't.
func TestQueuedNoSteerDetailOnlyForAnOldAgy(t *testing.T) {
	if !strings.HasPrefix(queuedNoSteerDetail, "This version of Antigravity can't take a message during a reply") || !strings.Contains(queuedNoSteerDetail, "press Stop to send it now") {
		t.Fatalf("detail = %q", queuedNoSteerDetail)
	}
	old := true
	previous := agyTooOldToSteer
	agyTooOldToSteer = func(string) bool { return old }
	t.Cleanup(func() { agyTooOldToSteer = previous })

	waiting := PendingQueuedMessage{QueueReason: "running"}
	agy := QueryRequest{Provider: "agy-cli"}
	if !waitsOnOldAgy("s", waiting, agy) {
		t.Fatal("a message behind an old agy's reply does not say why it waits")
	}
	if waitsOnOldAgy("s", PendingQueuedMessage{QueueReason: "running", CanSteer: true}, agy) {
		t.Fatal("a message that can steer says it waits")
	}
	if waitsOnOldAgy("s", waiting, QueryRequest{Provider: "agy-cli", DeliveryMode: "queue"}) {
		t.Fatal("a message queued on purpose says it can't go in")
	}
	if waitsOnOldAgy("s", waiting, QueryRequest{Provider: "codex-cli"}) {
		t.Fatal("another app's message gets Antigravity's reason")
	}
	old = false
	if waitsOnOldAgy("s", waiting, agy) {
		t.Fatal("an agy 1.5+ chat says it can't take the message")
	}
}
