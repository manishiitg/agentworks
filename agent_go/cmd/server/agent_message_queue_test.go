package server

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

// #2955: queue holds across native busy turns and releases exactly one FIFO
// request at an observed idle boundary; ACK alone cannot release the next item.
func TestNativeQueuedRequestWaitsForIdleAndKeepsFIFO(t *testing.T) {
	now := time.Now().UTC()
	a := namedMessageAgent{ID: "agent", Attachment: &namedAgentAttachment{Activity: "busy", ActivityAt: now}}
	first := agentMessage{ID: "first", From: 0, QueuedRequest: true, MessageType: "request", Delivery: "stored", SentAt: now, AgentSequence: 1}
	second := first
	second.ID = "second"
	second.AgentSequence = 2
	c := agentConversation{Endpoints: [2]agentMessageEndpoint{{}, {NamedAgentID: a.ID}}, Messages: []agentMessage{first, second}}
	rows := []agentConversation{c}
	if nativeQueueRequestReady(a, rows, first) {
		t.Fatal("queued request entered a busy native turn")
	}
	a.Attachment.Activity = "idle"
	if !nativeQueueRequestReady(a, rows, first) || nativeQueueRequestReady(a, rows, second) {
		t.Fatal("native queue did not select only the FIFO head")
	}
	rows[0].Messages[0].Delivery = "delivered"
	rows[0].Messages[0].DeliveredAt = now.Add(time.Second)
	if nativeQueueRequestReady(a, rows, second) {
		t.Fatal("ACK alone released another queued request into the same turn")
	}
	a.Attachment.ActivityAt = now.Add(2 * time.Second)
	if !nativeQueueRequestReady(a, rows, second) {
		t.Fatal("next observed idle did not release queued work")
	}
	// Missing activity and an old pending item fall back one-at-a-time, not forever.
	a.Attachment.Activity = "unknown"
	if nativeQueueReleaseReason(a, second, now) != "activity_unknown" {
		t.Fatal("unknown activity stranded work")
	}
	a.Attachment.Activity = "busy"
	old := second
	old.SentAt = now.Add(-11 * time.Minute)
	if nativeQueueReleaseReason(a, old, now) != "queue_timeout" {
		t.Fatal("old queue item did not fall back to ask")
	}
	second.QueuedRequest = false
	a.Attachment.Activity = "busy"
	if !nativeQueueRequestReady(a, rows, second) {
		t.Fatal("queue timing changed ordinary ask/send delivery")
	}
}

func TestQueuedBridgeRequestIsNotBatchedOrAnAnswer(t *testing.T) {
	api := &StreamingAPI{}
	turn := queuedConversationTurn{Principal: queuedConversationPrincipal{DeliveredMessage: map[string]interface{}{"source": "agent_message", "queue_kind": true}}}
	if api.queuedBatchEligible(turn) {
		t.Fatal("separate queued work would combine into one turn")
	}
	now := time.Now().UTC()
	task := agentMessage{ID: "old", From: 0, Sequence: 1, MessageType: "request", Delivery: "delivered"}
	c := agentConversation{Messages: []agentMessage{task, {From: 1, Sequence: 2, QueuedRequest: true, MessageType: "request"}}}
	if state, _ := agentTaskState(c, task, now); state != "open" {
		t.Fatal("new queued work closed unrelated work", state)
	}
}

// #2955: idle app queue requests use the normal immediate-start path, not a
// pending native activity fence or an unconditional enqueue.
func TestQueuedAppMessageStartsImmediatelyWhenIdle(t *testing.T) {
	api := newConversationTurnQueueTestAPI(map[string]string{})
	record := ProductConversationRecord{SessionID: "recipient", ConversationKey: "crew:chat:recipient"}
	input := api.desktopAppMessageInput("owner", record, desktopAppIncoming{Own: true, QueuedRequest: true}, "new work")
	req := QueryRequest{AgentMode: "multi-agent", DeliveryMode: input.DeliveryMode}
	ctx := internalBotRequestContext(context.Background(), "owner")
	response := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/query", nil).WithContext(ctx)
	if api.queueOccupiedConversationTurn(response, request, "owner", record.SessionID, req) {
		t.Fatal("idle app queue request was deferred instead of admitted to start")
	}
	if shouldTryRetainedDeliveryBeforeQueue(ctx, req, record.SessionID) {
		t.Fatal("queue request could steer a retained turn")
	}
}
