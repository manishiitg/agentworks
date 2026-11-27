package server

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

// sessionEventEmitter implements virtualtools.SessionEventEmitter to emit
// blocking_human_feedback events for human-input tools.
type sessionEventEmitter struct {
	eventStore *events.EventStore
	sessionID  string
}

func (e *sessionEventEmitter) EmitBlockingHumanFeedback(requestID, question, contextText string, yesNoOnly bool, yesLabel, noLabel string, options ...string) {
	now := time.Now()
	eventData := &orchEvents.BlockingHumanFeedbackEvent{
		BaseEventData: unifiedevents.BaseEventData{
			Timestamp: now,
		},
		Question:      question,
		AllowFeedback: !yesNoOnly && len(options) == 0, // Allow text input when not yes/no and no options
		Context:       contextText,
		SessionID:     e.sessionID,
		RequestID:     requestID,
		YesNoOnly:     yesNoOnly,
		YesLabel:      yesLabel,
		NoLabel:       noLabel,
		Options:       options,
	}
	event := events.Event{
		ID:        fmt.Sprintf("%s_human_feedback_%d", e.sessionID, now.UnixNano()),
		Type:      "blocking_human_feedback",
		Timestamp: now,
		SessionID: e.sessionID,
		Data: &unifiedevents.AgentEvent{
			Type:      orchEvents.BlockingHumanFeedback,
			Timestamp: now,
			SessionID: e.sessionID,
			Component: "delegation",
			Data:      eventData,
		},
	}
	e.eventStore.AddEvent(e.sessionID, event)
	log.Printf("[HUMAN_FEEDBACK] Emitted blocking_human_feedback event (request_id: %s, session: %s)", requestID, e.sessionID)
}

// EmitHumanFeedbackResolved records a durable answer/expiry marker for a
// blocking request so restore does not show an answered prompt as pending.
func (e *sessionEventEmitter) EmitHumanFeedbackResolved(requestID, outcome string) {
	now := time.Now()
	e.eventStore.AddEvent(e.sessionID, events.Event{
		ID:        fmt.Sprintf("%s_human_feedback_resolved_%s", e.sessionID, requestID),
		Type:      "human_feedback_resolved",
		Timestamp: now,
		SessionID: e.sessionID,
		Data: &unifiedevents.AgentEvent{
			Type:      unifiedevents.EventType("human_feedback_resolved"),
			Timestamp: now,
			SessionID: e.sessionID,
			Component: "delegation",
			Data: events.NewGenericEventData("human_feedback_resolved", map[string]interface{}{
				"request_id": requestID,
				"outcome":    outcome,
				"session_id": e.sessionID,
			}),
		},
	})
}

// EmitProductInteraction publishes the identical event a product.yaml tool
// binding's runtime.Emit produces (see emitAgentProfileEvent), for the human
// tools that have no such binding to carry that callback. Product is left
// blank: a session belongs to exactly one product already, so a UI reading
// its own session's events has no need to filter by it.
func (e *sessionEventEmitter) EmitProductInteraction(kind string, payload map[string]interface{}) {
	now := time.Now()
	data := &orchEvents.ProductInteractionEvent{Kind: kind, Payload: payload}
	data.BaseEventData = unifiedevents.BaseEventData{Timestamp: now}
	eventType := string(data.GetEventType())
	e.eventStore.AddEvent(e.sessionID, events.Event{
		ID:        fmt.Sprintf("%s_%s_%d", e.sessionID, strings.ReplaceAll(eventType, ".", "_"), now.UnixNano()),
		Type:      eventType,
		Timestamp: now,
		SessionID: e.sessionID,
		Data: &unifiedevents.AgentEvent{
			Type:      unifiedevents.EventType(eventType),
			Timestamp: now,
			SessionID: e.sessionID,
			Data:      data,
		},
	})
}
