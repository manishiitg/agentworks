package mcpagent

import (
	"context"
	"testing"

	"github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// RTS 2026-10-27: a message queued while the agent replied was injected as an anonymous user_message, so the
// chat showed it twice (a new row plus the person's own bubble pinned at the end). It is recorded once, where
// the agent takes it, under the person's identity, so the chat matches the bubble it already shows.
func TestQueuedSteerIsRecordedOnceUnderThePersonsIdentity(t *testing.T) {
	capture := &canonicalTurnCapture{}
	a := &Agent{logger: loggerv2.NewDefault(), sessionID: "steer-identity", listeners: []AgentEventListener{capture}}
	a.openSteerQueue()
	// Two rapid messages with identical text stay two distinct rows.
	a.addSteerMessageWithID("is pr ready?", QueuedMessageIdentity{ID: "steer-1", ClientID: "sub-1"})
	a.addSteerMessageWithID("is pr ready?", QueuedMessageIdentity{ID: "steer-2", ClientID: "sub-2"})
	a.addSteerMessage("internal note")
	injectSteerMessages(context.Background(), a, nil, a.drainFinalSteerMessages(), 0, "test")
	var clients []string
	anonymous := 0
	for _, event := range capture.snapshot() {
		data, ok := event.Data.(*events.UserMessageEvent)
		if !ok {
			t.Fatalf("unexpected event %T", event.Data)
		}
		if client, _ := data.Metadata["client_message_id"].(string); client != "" {
			clients = append(clients, client)
		} else {
			anonymous++
		}
	}
	if len(clients) != 2 || clients[0] != "sub-1" || clients[1] != "sub-2" || anonymous != 1 {
		t.Fatalf("client ids=%v anonymous=%d; want sub-1, sub-2 in order and one row for the internal note", clients, anonymous)
	}
}

// Stop before the queue drains: the person's message is recorded once, as failed, so its row offers resend.
func TestStoppedResponseRecordsQueuedMessageAsFailed(t *testing.T) {
	capture := &canonicalTurnCapture{}
	a := &Agent{logger: loggerv2.NewDefault(), sessionID: "steer-stop", listeners: []AgentEventListener{capture}}
	a.openSteerQueue()
	a.addSteerMessageWithID("also check logs", QueuedMessageIdentity{ID: "steer-9", ClientID: "sub-9"})
	reportUnsentSteers(context.Background(), a, a.closeSteerQueue())
	got := capture.snapshot()
	if len(got) != 1 {
		t.Fatalf("events = %d, want one failed row", len(got))
	}
	data, ok := got[0].Data.(*events.UserMessageEvent)
	if !ok || data.Metadata["client_message_id"] != "sub-9" || data.Metadata["confirmation"] != "failed" {
		t.Fatalf("event = %#v", got[0].Data)
	}
	if a.addSteerMessageWithID("late", QueuedMessageIdentity{ID: "steer-10"}) {
		t.Fatal("a stopped turn's queue accepted new input")
	}
}
