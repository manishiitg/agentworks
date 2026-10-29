package events

import (
	"context"
	"fmt"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/logger"
	"github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// EventObserver implements AgentEventListener to capture agent events
type EventObserver struct {
	usageExecutionID string
	store            *EventStore
	sessionID        string
	logger           loggerv2.Logger
}

// NewEventObserver creates a new event observer
func NewEventObserver(store *EventStore, sessionID string) *EventObserver {
	return &EventObserver{
		store:     store,
		sessionID: sessionID,
		logger:    createDefaultLogger(),
	}
}

// NewEventObserverWithLogger creates a new event observer with an injected logger
func NewEventObserverWithLogger(store *EventStore, sessionID string, logger loggerv2.Logger, usageExecutionID ...string) *EventObserver {
	id := ""
	if len(usageExecutionID) > 0 {
		id = usageExecutionID[0]
	}
	return &EventObserver{
		usageExecutionID: id,
		store:            store,
		sessionID:        sessionID,
		logger:           logger,
	}
}

// HandleEvent processes agent events and stores them in the event store
func (eo *EventObserver) HandleEvent(ctx context.Context, event *events.AgentEvent) error {
	// Create the store event with only the original AgentEvent data
	storeEvent := Event{
		ID:        StableAgentEventID("observer:"+eo.sessionID, event),
		Type:      string(event.Type),
		Timestamp: event.Timestamp,
		SessionID: eo.sessionID,
		Data:      event, // Use only the original AgentEvent
	}

	if event.Type == events.UserMessage {
		storeEvent.UsageExecutionID = eo.usageExecutionID
	}

	// No special handling - pass event data directly to frontend
	// The frontend will handle content extraction from the original event data
	// This follows the unified event system principle from types-sync-design.md

	// Content and error are already set on storeEvent if needed

	// Store the event by sessionID
	return eo.store.AddEventChecked(eo.sessionID, storeEvent)
}

// Name returns the observer name
func (eo *EventObserver) Name() string {
	return fmt.Sprintf("event_observer_%s", eo.sessionID)
}

// createDefaultLogger creates a default logger for the event observer
func createDefaultLogger() loggerv2.Logger {
	loggerInstance, err := logger.CreateLogger("", "info", "text", true)
	if err != nil {
		// If we can't create a logger, create a minimal one that won't panic
		return &minimalLogger{}
	}
	return loggerInstance
}

// minimalLogger is a fallback logger that implements loggerv2.Logger
type minimalLogger struct{}

func (m *minimalLogger) Debug(msg string, fields ...loggerv2.Field)            {}
func (m *minimalLogger) Info(msg string, fields ...loggerv2.Field)             {}
func (m *minimalLogger) Warn(msg string, fields ...loggerv2.Field)             {}
func (m *minimalLogger) Error(msg string, err error, fields ...loggerv2.Field) {}
func (m *minimalLogger) Fatal(msg string, err error, fields ...loggerv2.Field) {}
func (m *minimalLogger) With(fields ...loggerv2.Field) loggerv2.Logger         { return m }
func (m *minimalLogger) Close() error                                          { return nil }

// ToolEventCallback is called when tool_call_start or tool_call_end events are observed.
// Parameters: toolCallID, toolName, eventType ("start"/"end"/"error"), duration (0 for start)
type ToolEventCallback func(toolCallID, toolName, eventType string, duration time.Duration)

// DBStoreFunc is a callback to persist an AgentEvent to the database.
// Used to avoid circular dependency between events and database packages.
type DBStoreFunc func(ctx context.Context, sessionID string, event *events.AgentEvent) error

// DelegationEventObserver implements AgentEventListener to capture sub-agent events
// It tags events with Component field and ParentID to enable hierarchical display in UI
type DelegationEventObserver struct {
	store                  *EventStore
	sessionID              string
	depth                  int
	delegationID           string
	delegationStartEventID string // ID of the delegation_start event (for parent_id linking)
	logger                 loggerv2.Logger
	OnToolEvent            ToolEventCallback // optional callback for tool call tracking
	DBStore                DBStoreFunc       // optional: persist tagged events to database
}

// NewDelegationEventObserver creates a new event observer for delegated sub-agents
func NewDelegationEventObserver(store *EventStore, sessionID string, depth int, delegationID string, logger loggerv2.Logger) *DelegationEventObserver {
	return &DelegationEventObserver{
		store:                  store,
		sessionID:              sessionID,
		depth:                  depth,
		delegationID:           delegationID,
		delegationStartEventID: fmt.Sprintf("%s_delegation_start_%s", sessionID, delegationID),
		logger:                 logger,
	}
}

// HandleEvent processes sub-agent events and stores them with delegation metadata
// Events are linked to the delegation_start event via ParentID for hierarchical display
func (deo *DelegationEventObserver) HandleEvent(ctx context.Context, event *events.AgentEvent) error {
	// Tag the event with delegation metadata
	// We modify a copy to avoid affecting the original event
	taggedEvent := *event
	taggedEvent.Component = fmt.Sprintf("delegation-%d", deo.depth)
	taggedEvent.HierarchyLevel = deo.depth + 1 // Sub-agent events are one level deeper
	taggedEvent.SessionID = deo.sessionID
	taggedEvent.CorrelationID = deo.delegationID      // Links all events in this delegation
	taggedEvent.ParentID = deo.delegationStartEventID // Makes events children of delegation_start

	// Create the store event with the tagged data. Keep the provider identity so
	// replay does not manufacture a second structured event.
	storeEvent := Event{
		ID:        StableAgentEventID("delegation:"+deo.delegationID, &taggedEvent),
		Type:      string(taggedEvent.Type),
		Timestamp: taggedEvent.Timestamp,
		SessionID: deo.sessionID,
		Data:      &taggedEvent,
	}

	// Store the event by sessionID
	if err := deo.store.AddEventChecked(deo.sessionID, storeEvent); err != nil {
		return err
	}

	// Also persist tagged event to database (for shared sessions / session restore)
	// Apply filter to avoid storing high-volume events
	if deo.DBStore != nil && shouldReturnEvent(storeEvent, false) {
		if err := deo.DBStore(ctx, deo.sessionID, &taggedEvent); err != nil {
			deo.logger.Warn(fmt.Sprintf("[DELEGATION] Failed to persist sub-agent event to DB: %v", err))
		}
	}

	// Notify tool event callback if set
	if deo.OnToolEvent != nil && event.Data != nil {
		switch d := event.Data.(type) {
		case *events.ToolCallStartEvent:
			deo.OnToolEvent(d.ToolCallID, d.ToolName, "start", 0)
		case *events.ToolCallEndEvent:
			deo.OnToolEvent(d.ToolCallID, d.ToolName, "end", d.Duration)
		case *events.ToolCallErrorEvent:
			deo.OnToolEvent(d.ToolCallID, d.ToolName, "error", d.Duration)
		}
	}

	return nil
}

// Name returns the observer name
func (deo *DelegationEventObserver) Name() string {
	return fmt.Sprintf("delegation_event_observer_%s_%s", deo.sessionID, deo.delegationID)
}
