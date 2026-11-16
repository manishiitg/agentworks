package server

import (
	"strings"
	"testing"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

// #2881: delivery must create a visible row without invoking the turn boundary;
// an ACK retry or rejected preparation must not lose the next-turn FYI.
func TestAppFYIDeliveryIsVisibleWithoutTurnAndRetainedUntilGeneration(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.eventStore = storeevents.NewEventStore(100)
	ctx := internalBotRequestContext(t.Context(), "owner")
	for i := 0; i < 2; i++ {
		if err := env.api.deliverPassiveAgentMessage(ctx, "owner", "chat", "message-one", "For your information: example", map[string]interface{}{"source": "agent_message", "sender_label": "Helper"}); err != nil {
			t.Fatal(err)
		}
	}
	events := env.api.eventStore.GetEvents("chat", storeevents.GetEventsOptions{}).Events
	if len(events) != 1 || events[0].Type != "user_message" {
		t.Fatalf("FYI not shown exactly once: %+v", events)
	}
	for i := 0; i < 2; i++ {
		text, ids, err := pendingPassiveAgentMessages(ctx, "owner", "chat")
		if err != nil || !strings.Contains(text, "example") || len(ids) != 1 {
			t.Fatalf("preparation lost notification: %s %v %v", text, ids, err)
		}
	}
	if text, _, err := pendingPassiveAgentMessages(ctx, "owner", "other-chat"); err != nil || text != "" {
		t.Fatal("notification crossed chats", err)
	}
	if err := ackPassiveAgentMessages(ctx, "owner", "chat", []string{"message-one"}); err != nil {
		t.Fatal(err)
	}
	// A late delivery ACK retry must not re-arm an already-read notification.
	if err := env.api.deliverPassiveAgentMessage(ctx, "owner", "chat", "message-one", "For your information: example", nil); err != nil {
		t.Fatal(err)
	}
	if text, _, err := pendingPassiveAgentMessages(ctx, "owner", "chat"); err != nil || text != "" {
		t.Fatal("read notification replayed", err)
	}
	state, line := receiptState(agentMessage{MessageType: "notification", Delivery: "delivered"}, nil, true)
	if state != "delivered" || !strings.Contains(line, "next turn") {
		t.Fatal("FYI receipt implies a model has read it", state, line)
	}
	if passiveAgentMessage(agentMessage{MessageType: "notification", TaskReplyTo: "request", ReplyStatus: "done"}) {
		t.Fatal("a request reply would not wake the asker")
	}
}
