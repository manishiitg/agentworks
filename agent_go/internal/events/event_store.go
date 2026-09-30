package events

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/events"
)

// NEVER_SHOW_EVENTS contains event types that should NEVER be shown in any mode
// These are filtered out at the polling layer, regardless of source (memory or database)
// This catches events that were stored before SKIP_EVENTS was added to the event bridge
var NEVER_SHOW_EVENTS = map[string]bool{
	// Tool extras - no UI component
	"tool_execution": true,
	// Cache events - all 9 (no UI needed)
	"cache_event":               true,
	"comprehensive_cache_event": true,
	"cache_hit":                 true,
	"cache_miss":                true,
	"cache_write":               true,
	"cache_expired":             true,
	"cache_cleanup":             true,
	"cache_error":               true,
	"cache_operation_start":     true,
	// MCP connection lifecycle - tracer spans only, never shown
	"mcp_server_connection_start": true,
	"mcp_server_connection_end":   true,
	// Streaming chunks — ephemeral, only useful in real-time via subscriber.
	// Excluding from GetEvents/polling prevents them from consuming the InitialEventsLimit (300)
	// and pushing important events (request_human_feedback, tool calls) out of range.
	// Subscribers still receive these in real-time (see AddEvent override).
	"streaming_start": true,
	"streaming_chunk": true,
	// streaming_end is also lifecycle-only. SSE subscribers and explicit SSE backfill
	// still receive it via IncludeStreaming, but normal event polling should not keep
	// a completed "Thinking" artifact alive in tree/flat event views.
	"streaming_end": true,
}

// HIDDEN_EVENTS contains additional event types hidden from UI display
// Note: user_message is NOT filtered — essential for conversation display on session restore
// system_prompt is stored in DB but hidden
// NOTE: agent_end is NOT filtered — the frontend needs it to clear
// the "Generating..." state and streaming text. Without it, the UI stays stuck.
// NOTE: llm_generation_end is NOT filtered — ChatInput uses it as fallback for the
// context usage circle (token_usage with conversation_total is not emitted by all providers).
// The frontend hides it from the event list display via its own HIDDEN_EVENTS.
// NOTE: agent_error and batch_execution_canceled are NOT filtered — both are
// user-visible terminal signals for flat and tree event views.
// NOTE: step_progress_updated was removed on 2026-09-22 (emitted but never
// read anywhere; canvas step status is restored from todo_task_step_completed
// by WorkflowLayout, not from this event).
var HIDDEN_EVENTS = map[string]bool{
	"llm_generation_start":      true,
	"llm_generation_with_retry": true,
	"conversation_start":        true,
	"conversation_turn":         true,
	"system_prompt":             true,
	"agent_start":               true,
}

// MaxPollingLimit is the maximum number of events returned in a single polling request
// This prevents fetching too many events at once, especially when switching event modes
const MaxPollingLimit = 1000 // Match frontend MAX_EVENTS limit

// InitialEventsLimit is the number of events returned when starting from the beginning (sinceIndex=0)
// This is used when switching event modes - show latest events first, then allow loading older events
// Set to 300 to accommodate parallel multi-agent workflows where hierarchy-defining events
// (orchestrator_agent_start, delegation_start) may be far back in the event stream
const InitialEventsLimit = 300

const (
	durableAppendSlowThreshold = 50 * time.Millisecond
)

// STRUCTURAL_EVENTS are required to preserve hierarchy and important terminal
// state even when the initial event window is capped.
var STRUCTURAL_EVENTS = map[string]bool{
	"agent_end":                    true,
	"agent_error":                  true,
	"background_agent_completed":   true,
	"background_agent_started":     true,
	"background_agent_terminated":  true,
	"batch_execution_canceled":     true,
	"blocking_human_feedback":      true,
	"context_cancelled":            true, //nolint:misspell // Event schema uses the British spelling.
	"coding_agent_background_task": true,
	"coding_agent_question":        true,
	"conversation_end":             true,
	"conversation_error":           true,
	"conversation_resumed":         true,
	"delegation_end":               true,
	"delegation_start":             true,
	"human_feedback_resolved":      true,
	"learn_code_script_execution":  true,
	"orchestrator_agent_end":       true,
	"orchestrator_agent_error":     true,
	"orchestrator_agent_start":     true,
	"orchestrator_end":             true,
	"plan_approval":                true,
	"pre_validation_completed":     true,
	"request_human_feedback":       true,
	"routing_evaluated":            true,
	"todo_task_route_selected":     true,
	"todo_task_step_completed":     true,
	"unified_completion":           true,
	"user_message":                 true,
	"workflow_error":               true,
}

// LEGACY_REMOVED_EVENT_TYPES tombstones wire names deleted in event-deletion
// batches 1-5. Rows already persisted in structured-chat-events.sqlite keep
// their old discriminator; without this filter they would consume the
// InitialEventsLimit window and render as Unknown Event Type cards after
// their renderer was deleted. Every name here was verified to have no
// current producer before tombstoning. Add future deletions here.
var LEGACY_REMOVED_EVENT_TYPES = map[string]bool{
	// Batches 1-3 (37).
	"agent_processing": true, "batch_execution_end": true, "batch_execution_start": true,
	"batch_group_end": true, "batch_group_start": true, "cache_cleanup": true,
	"cache_error": true, "cache_expired": true, "cache_hit": true, "cache_miss": true,
	"cache_operation_start": true, "cache_write": true, "comprehensive_cache": true,
	"context_canceled": true, "context_editing_completed": true, "context_editing_error": true,
	"decision_evaluated": true, "large_tool_output_server_unavailable": true,
	"learning_completed": true, "learning_failed": true, "learning_skipped": true,
	"live_execution_streaming": true, "mcp_server_connection_error": true,
	"mcp_server_discovery": true, "model_change": true, "orchestrator_error": true,
	"orchestrator_start": true, "phase_completed": true, "phase_started": true,
	"prerequisite_navigation": true, "step_execution_end": true, "step_execution_failed": true,
	"step_execution_start": true, "step_progress_updated": true, "throttling_detected": true,
	"todo_steps_extracted": true, "token_limit_exceeded": true,
	// Batch 4 (20).
	"error_detail": true, "json_validation_start": true, "json_validation_end": true,
	"llm_messages": true, "llm_token_usage": true, "performance": true,
	"streaming_error": true, "streaming_progress": true, "streaming_connection_lost": true,
	"tool_output": true, "tool_response": true, "tool_call_progress": true, "debug": true,
	"mcp_server_connection": true, "independent_steps_selected": true,
	"human_verification_response": true, "background_agent_failed": true,
	"workflow_start": true, "workflow_progress": true, "workflow_end": true,
	// Batch 5 (3): context summarization feature removed entirely.
	"context_summarization_started": true, "context_summarization_completed": true,
	"context_summarization_error": true,
}

// ShouldShowEvent checks if an event should be shown in the UI
func ShouldShowEvent(eventType string) bool {
	if eventType == "" {
		return false
	}
	// First check: NEVER show these events
	if NEVER_SHOW_EVENTS[eventType] {
		return false
	}
	// Legacy tombstones: deleted wire names still present in old persisted rows
	if LEGACY_REMOVED_EVENT_TYPES[eventType] {
		return false
	}
	// Filter out hidden events
	return !HIDDEN_EVENTS[eventType]
}

// IsTranscriptMessage distinguishes whole assistant messages from transient
// terminal snapshots and token deltas, even after JSON restoration.
func IsTranscriptMessage(event Event) bool {
	if event.Type != "streaming_chunk" || event.Data == nil {
		return false
	}
	var source, content string
	var delta, tool bool
	var metadata map[string]interface{}
	switch data := event.Data.Data.(type) {
	case *events.StreamingChunkEvent:
		if data == nil {
			return false
		}
		source, content, delta, tool, metadata = data.Source, data.Content, data.IsDelta, data.IsToolCall, data.Metadata
	default:
		payload := eventPayloadMap(&event)
		source, _ = payload["source"].(string)
		content, _ = payload["content"].(string)
		delta, _ = payload["is_delta"].(bool)
		tool, _ = payload["is_tool_call"].(bool)
		metadata, _ = payload["metadata"].(map[string]interface{})
	}
	return source == "transcript" && strings.TrimSpace(content) != "" && !delta && !tool && metadata["kind"] != "terminal" && metadata["replace"] != true
}

func shouldReturnEvent(event Event, includeStreaming bool) bool {
	eventType := event.Type
	if IsTranscriptMessage(event) {
		return true
	}
	if includeStreaming && (eventType == "streaming_start" || eventType == "streaming_chunk" || eventType == "streaming_end") {
		return true
	}
	return ShouldShowEvent(eventType)
}

func preserveInitialStructuralEvents(filteredEvents []Event, limit int) []Event {
	if limit <= 0 || len(filteredEvents) <= limit {
		return filteredEvents
	}

	startPos := len(filteredEvents) - limit
	result := make([]Event, 0, limit)
	for i, event := range filteredEvents {
		if i >= startPos || STRUCTURAL_EVENTS[event.Type] {
			result = append(result, event)
		}
	}
	return result
}

func pruneEventsForRetention(events []Event, maxEvents int) ([]Event, int) {
	if maxEvents <= 0 || len(events) <= maxEvents {
		return events, 0
	}

	toDrop := len(events) - maxEvents
	drop := make([]bool, len(events))
	dropped := 0

	// Prefer dropping old noisy/detail events first. Structural workflow
	// milestones are what make restored tree/activity views understandable, so
	// keep them whenever the buffer can make room by pruning tool/log noise.
	for i, event := range events {
		if dropped >= toDrop {
			break
		}
		if STRUCTURAL_EVENTS[event.Type] || IsTranscriptMessage(event) {
			continue
		}
		drop[i] = true
		dropped++
	}

	// If a session emits more structural events than the configured cap, fall
	// back to dropping the oldest remaining events so the bound still holds.
	for i := range events {
		if dropped >= toDrop {
			break
		}
		if drop[i] {
			continue
		}
		drop[i] = true
		dropped++
	}

	pruned := make([]Event, 0, maxEvents)
	for i, event := range events {
		if drop[i] {
			continue
		}
		pruned = append(pruned, event)
	}
	return pruned, dropped
}

// Event represents a generic event that can be stored and retrieved
// Both MCP agent and orchestrator events now use the same AgentEvent structure
type Event struct {
	ID                string             `json:"id"`
	Type              string             `json:"type"`
	Timestamp         time.Time          `json:"timestamp"`
	Data              *events.AgentEvent `json:"data,omitempty"` // Use AgentEvent directly - both systems compatible
	Error             string             `json:"error,omitempty"`
	SessionID         string             `json:"session_id,omitempty"`
	ExecutionID       string             `json:"execution_id,omitempty"`
	ParentExecutionID string             `json:"parent_execution_id,omitempty"`
	ExecutionKind     string             `json:"execution_kind,omitempty"`
	TerminalOwnerID   string             `json:"terminal_owner_id,omitempty"`
	TerminalID        string             `json:"terminal_id,omitempty"`
	Sequence          int64              `json:"sequence,omitempty"`
}

// UnmarshalJSON restores the EventData interface as a lossless generic payload.
// Without this, reading a persisted UI trace fails on every non-empty event.
func (e *Event) UnmarshalJSON(raw []byte) error {
	type eventAlias Event
	var envelope struct {
		eventAlias
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	*e = Event(envelope.eventAlias)
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	type agentAlias events.AgentEvent
	var agent struct {
		agentAlias
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(envelope.Data, &agent); err != nil {
		return err
	}
	restored := events.AgentEvent(agent.agentAlias)
	if agent.Data != nil {
		restored.Data = NewGenericEventData(string(restored.Type), agent.Data)
	}
	e.Data = &restored
	return nil
}

// MarshalJSON customizes JSON serialization to flatten the event structure for frontend
func (e Event) MarshalJSON() ([]byte, error) {
	// Create a map with all the base fields
	result := map[string]interface{}{
		"id":         e.ID,
		"type":       e.Type,
		"timestamp":  e.Timestamp,
		"session_id": e.SessionID,
	}

	// Add error if it exists
	if e.Error != "" {
		result["error"] = e.Error
	}
	if e.ExecutionID != "" {
		result["execution_id"] = e.ExecutionID
	}
	if e.ParentExecutionID != "" {
		result["parent_execution_id"] = e.ParentExecutionID
	}
	if e.ExecutionKind != "" {
		result["execution_kind"] = e.ExecutionKind
	}
	if e.TerminalOwnerID != "" {
		result["terminal_owner_id"] = e.TerminalOwnerID
	}
	if e.TerminalID != "" {
		result["terminal_id"] = e.TerminalID
	}
	if e.Sequence > 0 {
		result["sequence"] = e.Sequence
	}

	// Add the original data field - this is the only data structure we use now
	if e.Data != nil {
		result["data"] = e.Data
	}

	return json.Marshal(result)
}

func (e *Event) ensureExecutionOwnership(sessionID string, previous []Event) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(e.SessionID)
	}
	if sessionID == "" {
		return
	}
	if e.SessionID == "" {
		e.SessionID = sessionID
	}

	if e.ExecutionID == "" {
		if inherited := findParentExecutionOwnership(e, previous); inherited != nil {
			e.ExecutionID = inherited.ExecutionID
			e.ParentExecutionID = inherited.ParentExecutionID
			e.ExecutionKind = inherited.ExecutionKind
			return
		}
	}

	payload := eventPayloadMap(e)
	metadata := mapField(payload, "metadata")
	correlationID := firstNonEmptyString(agentEventCorrelationID(e), stringField(payload, "correlation_id"))
	delegationID := firstNonEmptyString(stringField(payload, "delegation_id"), correlationID)
	backgroundAgentID := firstNonEmptyString(
		stringField(payload, "background_agent_id"),
		stringField(payload, "agent_id"),
		stringField(metadata, "background_agent_id"),
		stringField(metadata, "agent_id"),
		stringField(metadata, "execution_owner_id"),
	)
	parentExecutionID := firstNonEmptyString(
		stringField(metadata, "parent_execution_id"),
		stringField(payload, "parent_execution_id"),
	)
	autoNotificationExecutionID := autoNotificationExecutionID(e)
	workflowID := firstNonEmptyString(
		backgroundAgentID,
		autoNotificationExecutionID,
		normalizeWorkshopExecutionID(stringField(metadata, "workshop_step_id")),
		normalizeWorkshopExecutionID(stringField(payload, "workshop_step_id")),
		stringField(metadata, "workflow_run_id"),
		stringField(payload, "workflow_run_id"),
		stringField(metadata, "workflow_id"),
		stringField(payload, "workflow_id"),
		normalizeWorkshopExecutionID(correlationID),
	)
	stepID := firstNonEmptyString(
		stringField(metadata, "current_step_id"),
		stringField(metadata, "orchestrator_step_id"),
		stringField(metadata, "workflow_step_id"),
		stringField(metadata, "step_id"),
		stringField(metadata, "route_id"),
		stringField(payload, "current_step_id"),
		stringField(payload, "orchestrator_step_id"),
		stringField(payload, "workflow_step_id"),
		stringField(payload, "step_id"),
		stringField(payload, "route_id"),
	)

	if e.ExecutionID == "" {
		switch {
		case e.Type == "delegation_start" || e.Type == "delegation_end" || strings.HasPrefix(delegationID, "delegation-"):
			if delegationID != "" {
				e.ExecutionID = "delegation:" + delegationID
				e.ParentExecutionID = firstNonEmptyString(parentExecutionID, backgroundAgentID, "main:"+sessionID)
				e.ExecutionKind = "delegation"
			}
		case strings.HasPrefix(e.Type, "background_agent_") && backgroundAgentID != "":
			e.ExecutionID = backgroundAgentID
			e.ParentExecutionID = firstNonEmptyString(parentExecutionID, "main:"+sessionID)
			// Honor a kind the creator declared on the payload. Hardcoding
			// "background_agent" here discarded it, which is precisely what
			// BackgroundAgentStartedEvent.Kind documents must not happen: a
			// full run declares ExecutionKindFullRun so consumers know it is a
			// CONTAINER with no conversation of its own, and flattening that to
			// background_agent made it render as if it were an agent.
			e.ExecutionKind = firstNonEmptyString(
				stringField(payload, "execution_kind"),
				"background_agent",
			)
		case e.Type == "auto_notification_steered" && backgroundAgentID != "":
			e.ExecutionID = backgroundAgentID
			e.ParentExecutionID = firstNonEmptyString(parentExecutionID, "main:"+sessionID)
			e.ExecutionKind = firstNonEmptyString(
				stringField(payload, "execution_kind"),
				inferExecutionKind(backgroundAgentID, sessionID),
			)
		case autoNotificationExecutionID != "":
			e.ExecutionID = autoNotificationExecutionID
			e.ParentExecutionID = "main:" + sessionID
			e.ExecutionKind = firstNonEmptyString(
				stringField(payload, "execution_kind"),
				inferExecutionKind(autoNotificationExecutionID, sessionID),
			)
		case stepID != "":
			if parentExecutionID != "" {
				e.ExecutionID = "workflow-step:" + parentExecutionID + ":" + stepID
				e.ParentExecutionID = parentExecutionID
			} else if workflowID != "" {
				e.ExecutionID = "workflow-step:" + workflowID + ":" + stepID
				e.ParentExecutionID = workflowID
			} else {
				e.ExecutionID = "workflow-step:" + stepID
				e.ParentExecutionID = "main:" + sessionID
			}
			e.ExecutionKind = "workflow_step"
		case e.Type == "orchestrator_agent_start" || e.Type == "orchestrator_agent_end" || e.Type == "orchestrator_agent_error":
			switch {
			case parentExecutionID != "":
				e.ExecutionID = parentExecutionID
				e.ParentExecutionID = "main:" + sessionID
				e.ExecutionKind = inferExecutionKind(parentExecutionID, sessionID)
			case workflowID != "":
				e.ExecutionID = workflowID
				e.ParentExecutionID = "main:" + sessionID
				e.ExecutionKind = "workflow"
			case correlationID != "":
				e.ExecutionID = "agent:" + correlationID
				e.ParentExecutionID = "main:" + sessionID
				e.ExecutionKind = "agent"
			}
		case workflowID != "":
			e.ExecutionID = workflowID
			e.ParentExecutionID = "main:" + sessionID
			e.ExecutionKind = "workflow"
		default:
			e.ExecutionID = "main:" + sessionID
			e.ParentExecutionID = "session:" + sessionID
			e.ExecutionKind = "main_agent"
		}
	}

	if e.ParentExecutionID == "" {
		if e.ExecutionID == "main:"+sessionID {
			e.ParentExecutionID = "session:" + sessionID
		} else {
			e.ParentExecutionID = "main:" + sessionID
		}
	}
	if e.ExecutionKind == "" {
		e.ExecutionKind = inferExecutionKind(e.ExecutionID, sessionID)
	}
}

func findParentExecutionOwnership(event *Event, previous []Event) *Event {
	if event == nil || event.Data == nil || event.Data.ParentID == "" {
		return nil
	}
	parentID := event.Data.ParentID
	for i := len(previous) - 1; i >= 0; i-- {
		prev := previous[i]
		if prev.ExecutionID == "" {
			continue
		}
		if prev.ID == parentID || (prev.Data != nil && prev.Data.SpanID == parentID) {
			return &prev
		}
	}
	return nil
}

func eventPayloadMap(event *Event) map[string]interface{} {
	if event == nil || event.Data == nil || event.Data.Data == nil {
		return nil
	}
	raw, err := json.Marshal(event.Data.Data)
	if err != nil {
		return nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	if nested := mapField(out, "data"); nested != nil {
		for key, value := range nested {
			if _, exists := out[key]; !exists {
				out[key] = value
			}
		}
	}
	return out
}

func agentEventCorrelationID(event *Event) string {
	if event == nil || event.Data == nil {
		return ""
	}
	return strings.TrimSpace(event.Data.CorrelationID)
}

func autoNotificationExecutionID(event *Event) string {
	if event == nil || event.Type != "user_message" {
		return ""
	}
	payload := eventPayloadMap(event)
	content := firstNonEmptyString(
		stringField(payload, "content"),
		stringField(payload, "message"),
	)
	if !strings.HasPrefix(content, "[AUTO-NOTIFICATION]") {
		return ""
	}
	const marker = "(ID:"
	start := strings.Index(content, marker)
	if start < 0 {
		return ""
	}
	start += len(marker)
	end := strings.Index(content[start:], ")")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(content[start : start+end])
}

func stringField(record map[string]interface{}, key string) string {
	if record == nil {
		return ""
	}
	value, ok := record[key]
	if !ok {
		return ""
	}
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func mapField(record map[string]interface{}, key string) map[string]interface{} {
	if record == nil {
		return nil
	}
	value, ok := record[key]
	if !ok {
		return nil
	}
	if nested, ok := value.(map[string]interface{}); ok {
		return nested
	}
	return nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeWorkshopExecutionID(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "workshop-workflow-") {
		return strings.TrimPrefix(value, "workshop-")
	}
	if strings.HasPrefix(value, "workflow-full-") || strings.HasPrefix(value, "workflow-step-") {
		return value
	}
	return ""
}

func inferExecutionKind(executionID, sessionID string) string {
	switch {
	case executionID == "main:"+sessionID:
		return "main_agent"
	case strings.HasPrefix(executionID, "delegation:"):
		return "delegation"
	case strings.HasPrefix(executionID, "agent:"):
		return "agent"
	case strings.HasPrefix(executionID, "workflow-step:"):
		return "workflow_step"
	case strings.HasPrefix(executionID, "workflow:"):
		return "workflow"
	case strings.HasPrefix(executionID, "workflow-"):
		return "workflow"
	default:
		return "execution"
	}
}

// ActivityCallback is called when an event is added to update session activity
type ActivityCallback func(sessionID string)

// EventAddedCallback receives the stored/enriched event after AddEvent accepts it.
// It runs outside the EventStore lock and must not mutate the event.
type EventAddedCallback func(sessionID string, event Event)

// Subscriber represents a client subscribed to real-time events for a session via SSE.
type Subscriber struct {
	Ch        chan Event
	SessionID string
}

// EventStore manages in-memory event storage for sessions
// Events are stored by sessionID, allowing multiple observers to view the same session
type EventStore struct {
	events              map[string][]Event // sessionID -> events
	terminalEvents      map[string]map[string][]Event
	nextSequence        map[string]int64
	sessionStartIndices map[string]int    // sessionID -> startIndex (offset for events in memory)
	sessionOwners       map[string]string // sessionID -> userID
	persistenceClasses  map[string]SessionPersistenceClass
	// expectedClientMessages: sessionID -> client identity for the next
	// main-agent user_message (see ExpectClientUserMessage). Guarded by mu.
	expectedClientMessages map[string]expectedClientMessage
	mu                     sync.RWMutex
	maxEvents              int // Maximum events per session
	cleanupTicker          *time.Ticker
	stopCh                 chan struct{}
	// journalMaintenanceStarted guards the single compaction/size-guard loop.
	journalMaintenanceStarted bool
	// steerOrdering holds answer rows behind a deferred steered message.
	steerOrderingOnce  sync.Once
	steerOrdering      *steerOrdering
	activityCallback   ActivityCallback // Optional callback to update session activity
	eventAddedCallback EventAddedCallback
	durableJournal     DurableEventJournal
	hydratedSessions   map[string]bool
	hydrateMu          sync.Mutex
	// journalProbed caches "journal has no history for this session" so an
	// unclassified live-only session pays for the probe once, not per event.
	journalProbed map[string]bool

	// appendLocks serialize AddEvent per session so the durable SQLite write
	// can run outside mu: one session's fsync must not stall every other
	// session's AddEvent and GetEvents.
	// Entries are reference-counted and removed once no caller holds or waits
	// on them, so the map does not grow with every session ever seen.
	appendLocks   map[string]*sessionAppendLockEntry
	appendLocksMu sync.Mutex

	// SSE subscriber registry: sessionID -> list of subscribers
	subscribers   map[string][]*Subscriber
	subscribersMu sync.RWMutex
}

// NewEventStore creates a new event store with configurable limits
func NewEventStore(maxEvents int) *EventStore {
	return NewEventStoreWithActivityCallback(maxEvents, nil)
}

// NewEventStoreWithActivityCallback creates a new event store with an activity callback
func NewEventStoreWithActivityCallback(maxEvents int, activityCallback ActivityCallback) *EventStore {
	store := &EventStore{
		events:              make(map[string][]Event),
		terminalEvents:      make(map[string]map[string][]Event),
		nextSequence:        make(map[string]int64),
		sessionStartIndices: make(map[string]int),
		sessionOwners:       make(map[string]string),
		persistenceClasses:  make(map[string]SessionPersistenceClass),
		maxEvents:           maxEvents,
		cleanupTicker:       time.NewTicker(5 * time.Minute), // Cleanup every 5 minutes
		stopCh:              make(chan struct{}),
		activityCallback:    activityCallback,
		hydratedSessions:    make(map[string]bool),
		journalProbed:       make(map[string]bool),
		appendLocks:         make(map[string]*sessionAppendLockEntry),
		subscribers:         make(map[string][]*Subscriber),
	}

	// Start background cleanup
	go store.cleanupRoutine()

	return store
}

// SetDurableJournal makes structured-event persistence part of AddEvent's
// acceptance boundary. Configure it before producers start adding events.
func (es *EventStore) SetDurableJournal(journal DurableEventJournal) {
	es.mu.Lock()
	defer es.mu.Unlock()
	es.durableJournal = journal
	if _, compacts := journal.(DurableEventJournalCompactor); compacts && !es.journalMaintenanceStarted {
		es.journalMaintenanceStarted = true
		go es.runJournalMaintenanceLoop(LoadJournalMaintenanceConfig())
	}
}

func (es *EventStore) hydrateSession(sessionID string) {
	if es == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	es.adoptJournaledSession(sessionID)
	if !es.sessionUsesDurableChatJournal(sessionID) {
		return
	}
	es.hydrateMu.Lock()
	defer es.hydrateMu.Unlock()
	es.mu.RLock()
	journal := es.durableJournal
	alreadyHydrated := es.hydratedSessions[sessionID]
	es.mu.RUnlock()
	if journal == nil || alreadyHydrated {
		return
	}
	persisted, err := journal.LoadTail(sessionID, es.maxEvents)
	if err != nil {
		log.Printf("[EventStore] durable replay failed for session %s: %v", sessionID, err)
		return
	}
	es.mu.Lock()
	defer es.mu.Unlock()
	if es.hydratedSessions[sessionID] {
		return
	}
	if len(persisted) > 0 {
		es.events[sessionID] = append([]Event(nil), persisted...)
		// Sequence is one-based and gap-free within the durable journal. When
		// only the bounded tail is hydrated, preserve its absolute zero-based
		// polling position so a reconnect cursor is not compared with tail-local
		// slice indexes.
		if persisted[0].Sequence > 1 {
			es.sessionStartIndices[sessionID] = int(persisted[0].Sequence - 1)
		}
		for _, event := range persisted {
			if event.Sequence > es.nextSequence[sessionID] {
				es.nextSequence[sessionID] = event.Sequence
			}
		}
		es.rebuildTerminalEventsLocked(sessionID)
	} else if _, exists := es.events[sessionID]; !exists {
		es.events[sessionID] = make([]Event, 0)
	}
	es.hydratedSessions[sessionID] = true
}

// SetSessionOwner records the user that owns a session's in-memory events.
func (es *EventStore) SetSessionOwner(sessionID, userID string) {
	sessionID = strings.TrimSpace(sessionID)
	userID = strings.TrimSpace(userID)
	if sessionID == "" || userID == "" {
		return
	}

	es.mu.Lock()
	es.sessionOwners[sessionID] = userID
	journal := es.durableJournal
	class := es.persistenceClasses[sessionID]
	es.mu.Unlock()
	if class == SessionPersistenceInteractiveChat {
		if ownership, ok := journal.(DurableEventJournalOwnership); ok {
			if err := ownership.RegisterOwner(sessionID, userID); err != nil {
				log.Printf("[EventStore] durable chat owner registration failed session=%s: %v", sessionID, err)
			}
		}
	}
}

// GetSessionOwner returns the user that owns a session's in-memory events.
func (es *EventStore) GetSessionOwner(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}

	es.mu.RLock()
	defer es.mu.RUnlock()
	return es.sessionOwners[sessionID]
}

// SetActivityCallback sets the activity callback (can be called after creation)
func (es *EventStore) SetActivityCallback(callback ActivityCallback) {
	es.mu.Lock()
	defer es.mu.Unlock()
	es.activityCallback = callback
}

// SetEventAddedCallback sets a runtime callback for sidecar stores that should
// observe accepted events without becoming part of durable event history.
func (es *EventStore) SetEventAddedCallback(callback EventAddedCallback) {
	es.mu.Lock()
	defer es.mu.Unlock()
	es.eventAddedCallback = callback
}

// Subscribe creates a new subscriber for real-time events on a session.
// The returned Subscriber's Ch channel receives events as they are added.
// Buffer size is 256 to absorb bursts without blocking AddEvent.
func (es *EventStore) Subscribe(sessionID string) *Subscriber {
	sub := &Subscriber{
		Ch:        make(chan Event, 256),
		SessionID: sessionID,
	}
	es.subscribersMu.Lock()
	es.subscribers[sessionID] = append(es.subscribers[sessionID], sub)
	es.subscribersMu.Unlock()
	return sub
}

// Unsubscribe removes a subscriber and closes its channel.
func (es *EventStore) Unsubscribe(sessionID string, sub *Subscriber) {
	es.subscribersMu.Lock()
	defer es.subscribersMu.Unlock()
	subs := es.subscribers[sessionID]
	for i, s := range subs {
		if s == sub {
			es.subscribers[sessionID] = append(subs[:i], subs[i+1:]...)
			close(sub.Ch)
			return
		}
	}
}

type sessionAppendLockEntry struct {
	mu   sync.Mutex
	refs int // holders plus waiters, guarded by EventStore.appendLocksMu
}

// lockSessionAppend acquires the session's append lock and returns its release.
// A caller registers on the entry before blocking, and the entry is deleted
// only when no holder or waiter remains, so two appends for one session can
// never run under different mutexes.
func (es *EventStore) lockSessionAppend(sessionID string) func() {
	es.appendLocksMu.Lock()
	entry := es.appendLocks[sessionID]
	if entry == nil {
		entry = &sessionAppendLockEntry{}
		es.appendLocks[sessionID] = entry
	}
	entry.refs++
	es.appendLocksMu.Unlock()

	entry.mu.Lock()
	var once sync.Once
	return func() {
		once.Do(func() {
			entry.mu.Unlock()
			es.appendLocksMu.Lock()
			entry.refs--
			if entry.refs == 0 && es.appendLocks[sessionID] == entry {
				delete(es.appendLocks, sessionID)
			}
			es.appendLocksMu.Unlock()
		})
	}
}

// adoptJournaledSession classifies an unclassified session as an interactive
// chat when the durable journal already holds its history. After a restart or
// buffer eviction the first event may come from a pane observer or background
// notification rather than the query handler; without this the chat would stay
// live-only for the life of the process and its restore would be refused.
func (es *EventStore) adoptJournaledSession(sessionID string) {
	if es == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	es.mu.RLock()
	_, classified := es.persistenceClasses[sessionID]
	probed := es.journalProbed[sessionID]
	probe, ok := es.durableJournal.(DurableEventJournalSessionProbe)
	es.mu.RUnlock()
	if classified || probed || !ok {
		return
	}
	known, lastSequence, err := probe.KnownSession(sessionID)
	if err != nil {
		log.Printf("[EventStore] durable session probe failed session=%s: %v", sessionID, err)
		return
	}
	es.mu.Lock()
	defer es.mu.Unlock()
	es.journalProbed[sessionID] = true
	if !known {
		return
	}
	if _, classified := es.persistenceClasses[sessionID]; classified {
		return
	}
	es.persistenceClasses[sessionID] = SessionPersistenceInteractiveChat
	// Eviction keeps a hydration tombstone, so seed the live sequence from the
	// journal: new live-only events must not number below the durable tip.
	if lastSequence > es.nextSequence[sessionID] {
		es.nextSequence[sessionID] = lastSequence
	}
}

// AddEvent adds an event for a specific session. Legacy callers that cannot
// propagate an observer error retain this compatibility entry point; durable
// failures are logged and the event is not published.
func (es *EventStore) AddEvent(sessionID string, event Event) {
	_ = es.AddEventChecked(sessionID, event)
}

// AddEventChecked adds an event and reports whether it crossed the durable
// acceptance boundary. Canonical producer bridges use this method so a failed
// append can stop or retry upstream delivery.
func (es *EventStore) AddEventChecked(sessionID string, event Event) error {
	if es.holdForDeferredSteer(sessionID, event) {
		return nil
	}
	err := es.addEventUnheld(sessionID, event)
	if err == nil {
		es.signalAnswerEnded(sessionID, event)
	}
	return err
}

func (es *EventStore) addEventUnheld(sessionID string, event Event) error {
	es.hydrateSession(sessionID)
	// The per-session lock spans sequence assignment, the durable append and the
	// in-memory insert, so a session's events stay ordered while mu is released
	// around SQLite I/O. It is released before callbacks and SSE fan-out, as mu
	// was before, so a callback that adds another event cannot deadlock.
	unlockSession := es.lockSessionAppend(sessionID)
	es.mu.Lock()
	if event.Data != nil {
		if cloned := events.CloneAgentEvent(event.Data); cloned != nil {
			event.Data = cloned
		}
	}
	if strings.TrimSpace(event.ID) == "" {
		event.ID = StableAgentEventID("event-store:"+sessionID, event.Data)
	}

	// Initialize session if not exists
	if _, exists := es.events[sessionID]; !exists {
		es.events[sessionID] = make([]Event, 0)
		es.sessionStartIndices[sessionID] = 0
	}
	event.ensureExecutionOwnership(sessionID, es.events[sessionID])
	es.stampExpectedClientUserMessage(sessionID, &event)
	if event.TerminalOwnerID == "" {
		event.TerminalOwnerID = ResolveTerminalOwnerID(sessionID, event, nil)
	}
	if event.TerminalID == "" {
		event.TerminalID = TerminalIDForOwner(sessionID, event.TerminalOwnerID)
	}
	if event.Sequence <= 0 {
		es.nextSequence[sessionID]++
		event.Sequence = es.nextSequence[sessionID]
	} else if event.Sequence > es.nextSequence[sessionID] {
		es.nextSequence[sessionID] = event.Sequence
	}
	projected, durable := projectDurableChatEvent(event)
	journal := es.durableJournal
	if journal != nil && es.persistenceClasses[sessionID] == SessionPersistenceInteractiveChat && durable {
		es.mu.Unlock()
		started := time.Now()
		persisted, inserted, err := journal.Append(sessionID, projected)
		elapsed := time.Since(started)
		if elapsed >= durableAppendSlowThreshold {
			log.Printf("[EventStore] slow durable append session=%s type=%s id=%s duration=%s", sessionID, event.Type, event.ID, elapsed.Round(time.Millisecond))
		}
		if err != nil {
			unlockSession()
			log.Printf("[EventStore] durable append failed; event not published session=%s type=%s id=%s: %v", sessionID, event.Type, event.ID, err)
			return err
		}
		if !inserted {
			unlockSession()
			return nil
		}
		event = persisted
		es.mu.Lock()
		if _, exists := es.events[sessionID]; !exists {
			es.events[sessionID] = make([]Event, 0)
			es.sessionStartIndices[sessionID] = 0
		}
	}
	if event.Sequence > es.nextSequence[sessionID] {
		es.nextSequence[sessionID] = event.Sequence
	}

	// Add event
	es.events[sessionID] = append(es.events[sessionID], event)
	if event.TerminalID != "" {
		if es.terminalEvents[sessionID] == nil {
			es.terminalEvents[sessionID] = make(map[string][]Event)
		}
		owned := append(es.terminalEvents[sessionID][event.TerminalID], event)
		if es.maxEvents > 0 && len(owned) > es.maxEvents {
			owned, _ = pruneEventsForRetention(owned, es.maxEvents)
		}
		es.terminalEvents[sessionID][event.TerminalID] = owned
	}

	// Remove old events if over limit
	if len(es.events[sessionID]) > es.maxEvents {
		var droppedCount int
		es.events[sessionID], droppedCount = pruneEventsForRetention(es.events[sessionID], es.maxEvents)
		// Update start index to reflect dropped events
		es.sessionStartIndices[sessionID] += droppedCount
		// The terminal index is an alternate view over the bounded session
		// working set, not a second unbounded history store. Rebuild after global
		// retention so unopened terminals cannot keep otherwise-evicted payloads
		// alive in backend memory.
		es.rebuildTerminalEventsLocked(sessionID)
	}

	// Call activity callback if set (call outside of lock to avoid deadlock)
	activityCallback := es.activityCallback
	eventAddedCallback := es.eventAddedCallback
	es.mu.Unlock()
	unlockSession()

	// Update session activity (call outside lock to avoid potential deadlock)
	if activityCallback != nil && sessionID != "" {
		activityCallback(sessionID)
	}
	if eventAddedCallback != nil && sessionID != "" {
		eventAddedCallback(sessionID, event)
	}

	logStreamingChunkTelemetry(sessionID, event)
	es.logToolCallTelemetry(sessionID, event)

	// Notify SSE subscribers (non-blocking send; drop if buffer full)
	es.subscribersMu.RLock()
	subs := es.subscribers[sessionID]
	for _, sub := range subs {
		// Apply event filtering for subscriber.
		// Exception: streaming events are always delivered to subscribers for real-time display,
		// even though they're in NEVER_SHOW_EVENTS (excluded from GetEvents backfill/polling).
		isStreamingEvent := event.Type == "streaming_start" || event.Type == "streaming_chunk" || event.Type == "streaming_end"
		if !isStreamingEvent && !ShouldShowEvent(event.Type) {
			continue
		}
		select {
		case sub.Ch <- event:
		default:
			// Channel full, drop event (subscriber will catch up via backfill)
			log.Printf("[EventStore] WARNING: channel full, dropping %s for session %s", event.Type, sessionID)
		}
	}
	es.subscribersMu.RUnlock()
	return nil
}

func (es *EventStore) rebuildTerminalEventsLocked(sessionID string) {
	indexed := make(map[string][]Event)
	for _, event := range es.events[sessionID] {
		if event.TerminalID == "" {
			continue
		}
		indexed[event.TerminalID] = append(indexed[event.TerminalID], event)
	}
	es.terminalEvents[sessionID] = indexed
}

// ToolCallSummary holds a compact representation of a tool call event.
type ToolCallSummary struct {
	ToolCallID string        `json:"tool_call_id"`
	ToolName   string        `json:"tool_name"`
	Status     string        `json:"status"` // "running", "done", "error"
	Duration   time.Duration `json:"duration,omitempty"`
	StartedAt  time.Time     `json:"started_at"`
	Args       string        `json:"args,omitempty"`   // full arguments from ToolCallStartEvent
	Result     string        `json:"result,omitempty"` // full result from ToolCallEndEvent (or error message)
}

// GetToolCallsByCorrelation returns tool call summaries for events matching the given correlationID.
// Used by query_step to show which tools a running step is calling.
func (es *EventStore) GetToolCallsByCorrelation(sessionID, correlationID string) []ToolCallSummary {
	es.hydrateSession(sessionID)
	es.mu.RLock()
	defer es.mu.RUnlock()

	evts, exists := es.events[sessionID]
	if !exists {
		return nil
	}

	// Track tool calls: toolCallID -> summary
	toolCalls := make(map[string]*ToolCallSummary)
	var order []string // preserve insertion order

	for _, evt := range evts {
		if evt.Data == nil || evt.Data.CorrelationID != correlationID {
			continue
		}
		switch d := evt.Data.Data.(type) {
		case *events.ToolCallStartEvent:
			if _, exists := toolCalls[d.ToolCallID]; !exists {
				order = append(order, d.ToolCallID)
			}
			toolCalls[d.ToolCallID] = &ToolCallSummary{
				ToolCallID: d.ToolCallID,
				ToolName:   d.ToolName,
				Status:     "running",
				StartedAt:  evt.Timestamp,
				Args:       d.ToolParams.Arguments,
			}
		case *events.ToolCallEndEvent:
			if tc, ok := toolCalls[d.ToolCallID]; ok {
				tc.Status = "done"
				tc.Duration = d.Duration
				tc.Result = d.Result
			} else {
				order = append(order, d.ToolCallID)
				toolCalls[d.ToolCallID] = &ToolCallSummary{
					ToolCallID: d.ToolCallID,
					ToolName:   d.ToolName,
					Status:     "done",
					Duration:   d.Duration,
					StartedAt:  evt.Timestamp,
					Result:     d.Result,
				}
			}
		case *events.ToolCallErrorEvent:
			if tc, ok := toolCalls[d.ToolCallID]; ok {
				tc.Status = "error"
				tc.Duration = d.Duration
				tc.Result = d.Error
			} else {
				order = append(order, d.ToolCallID)
				toolCalls[d.ToolCallID] = &ToolCallSummary{
					ToolCallID: d.ToolCallID,
					ToolName:   d.ToolName,
					Status:     "error",
					Duration:   d.Duration,
					StartedAt:  evt.Timestamp,
					Result:     d.Error,
				}
			}
		}
	}

	result := make([]ToolCallSummary, 0, len(order))
	for _, id := range order {
		result = append(result, *toolCalls[id])
	}
	return result
}

// GetAllEventsRaw returns a copy of all in-memory events for a session with no
// filtering and no limit. Used for persistence on session completion.
func (es *EventStore) GetAllEventsRaw(sessionID string) []Event {
	es.hydrateSession(sessionID)
	es.mu.RLock()
	defer es.mu.RUnlock()
	src, ok := es.events[sessionID]
	if !ok {
		return nil
	}
	out := make([]Event, len(src))
	copy(out, src)
	return out
}

// InitializeSession creates an empty event list for a session
func (es *EventStore) InitializeSession(sessionID string, baseIndex int) {
	es.hydrateSession(sessionID)
	es.mu.Lock()
	defer es.mu.Unlock()

	es.sessionStartIndices[sessionID] = baseIndex
	// Initialize session if not exists
	if _, exists := es.events[sessionID]; !exists {
		es.events[sessionID] = make([]Event, 0)
	}
}

// GetEventsOptions contains options for retrieving events
type GetEventsOptions struct {
	SinceIndex       int  // For forward polling: get events after this index
	Limit            int  // For pagination: maximum number of events to return (0 = no limit)
	Offset           int  // For pagination: skip this many events (used for backward pagination)
	IncludeStreaming bool // Include streaming_start/chunk/end for SSE reconnect backfill only
}

// GetEventsResult contains the result of GetEvents call
type GetEventsResult struct {
	Events             []Event
	Exists             bool
	TotalCount         int
	LastProcessedIndex int  // Last index processed in unfiltered array (for forward polling with filtering)
	HasMore            bool // Whether there are more events available (for initial fetch with sinceIndex=0)
}

// GetTerminalEventsOptions selects a stable page from one canonical terminal
// transcript. BeforeSequence and AfterSequence are exclusive global event
// cursors; at most one may be set.
type GetTerminalEventsOptions struct {
	Limit          int
	BeforeSequence int64
	AfterSequence  int64
}

// GetTerminalEventsResult is a cursor page for one terminal transcript.
type GetTerminalEventsResult struct {
	Events         []Event
	Exists         bool
	HasOlder       bool
	HasNewer       bool
	OldestSequence int64
	LatestSequence int64
}

// GetEvents retrieves events for a session with various options
// Supports both forward polling (sinceIndex) and backward pagination (limit/offset)
func (es *EventStore) GetEvents(sessionID string, opts GetEventsOptions) GetEventsResult {
	es.hydrateSession(sessionID)
	es.mu.RLock()
	defer es.mu.RUnlock()

	events, exists := es.events[sessionID]
	if !exists {
		return GetEventsResult{
			Events:             []Event{},
			Exists:             false,
			TotalCount:         0,
			LastProcessedIndex: -1,
			HasMore:            false,
		}
	}

	// Get the base index offset for this session (accounts for events stored in DB before reactivation)
	// This offset is set when a session is reactivated to track where in-memory events start
	// relative to the full event history (DB events + in-memory events)
	baseIndex := es.sessionStartIndices[sessionID]

	var result []Event
	var lastProcessedIndex int
	hasMore := false

	// Adjust sinceIndex to be relative to in-memory array if a base index is set
	// Frontend sends absolute index (including DB events), we need relative index for in-memory array
	adjustedSinceIndex := opts.SinceIndex
	if baseIndex > 0 && opts.SinceIndex >= 0 {
		adjustedSinceIndex = opts.SinceIndex - baseIndex
		// If sinceIndex is before our in-memory events start, return all in-memory events
		if adjustedSinceIndex < 0 {
			adjustedSinceIndex = -1 // Will trigger "get all events" mode below
		}
	}

	// Determine which events to retrieve based on options
	if adjustedSinceIndex >= 0 || (opts.SinceIndex >= 0 && adjustedSinceIndex == -1) {
		// Forward polling mode: get events after sinceIndex (adjusted for base index)
		// CRITICAL: Filter FIRST, then apply index logic
		// This ensures consistency with backward pagination and correct filtering behavior

		// If adjustedSinceIndex is -1 but we're in forward polling mode (sinceIndex >= 0),
		// it means sinceIndex was before our in-memory events - return all in-memory events
		effectiveSinceIndex := adjustedSinceIndex
		if effectiveSinceIndex < 0 {
			effectiveSinceIndex = -1 // Will return all events
		}

		// Step 1: Filter the entire array first
		filteredEvents := make([]Event, 0, len(events))
		for _, event := range events {
			if shouldReturnEvent(event, opts.IncludeStreaming) {
				filteredEvents = append(filteredEvents, event)
			}
		}

		// Step 2: Find the position in filtered array that corresponds to "after sinceIndex" in unfiltered array
		// Use effectiveSinceIndex (relative to in-memory array)
		filteredCountUpToSinceIndex := 0
		for i := 0; i <= effectiveSinceIndex && i < len(events); i++ {
			if shouldReturnEvent(events[i], opts.IncludeStreaming) {
				filteredCountUpToSinceIndex++
			}
		}

		// Step 3: Slice from the next position in filtered array
		if effectiveSinceIndex >= len(events) {
			result = []Event{}
			lastProcessedIndex = baseIndex + len(events) - 1
		} else if effectiveSinceIndex <= 0 && len(filteredEvents) > 0 {
			// Special case: Starting from beginning or before (sinceIndex=0 or adjusted to -1)
			// Return all filtered events (no InitialEventsLimit when continuing a conversation)
			// The limit only applies to initial page load, not to polling after reactivation
			if effectiveSinceIndex < 0 {
				// Return all in-memory events (sinceIndex was before our in-memory events)
				result = filteredEvents
				lastProcessedIndex = baseIndex + len(events) - 1
				hasMore = false
			} else {
				// sinceIndex=0: Apply initial limit while keeping older structural
				// events that are needed to render tree and flat context correctly.
				result = preserveInitialStructuralEvents(filteredEvents, InitialEventsLimit)
				lastProcessedIndex = baseIndex + len(events) - 1
				hasMore = len(result) < len(filteredEvents)
			}
		} else {
			nextFilteredPos := filteredCountUpToSinceIndex
			if nextFilteredPos >= len(filteredEvents) {
				result = []Event{}
				lastProcessedIndex = baseIndex + len(events) - 1
			} else {
				// Apply maximum limit to prevent fetching too many events at once
				remainingEvents := filteredEvents[nextFilteredPos:]
				if len(remainingEvents) > MaxPollingLimit {
					result = remainingEvents[:MaxPollingLimit]
					// Calculate actual last processed index (relative to in-memory array)
					filteredCount := 0
					actualLastIndex := -1
					for i := 0; i < len(events); i++ {
						if shouldReturnEvent(events[i], opts.IncludeStreaming) {
							filteredCount++
							if filteredCount == nextFilteredPos+MaxPollingLimit {
								actualLastIndex = i
								break
							}
						}
					}
					lastProcessedIndex = baseIndex + actualLastIndex
				} else {
					result = remainingEvents
					lastProcessedIndex = baseIndex + len(events) - 1
				}
			}
		}
	} else if opts.Limit > 0 {
		// Backward pagination mode: get older events using limit/offset
		// Events are stored chronologically (oldest to newest: index 0 = oldest)
		// For "load more", we want older events from the START of the array
		// Offset counts from the START (0 = oldest events)

		// CRITICAL: Filter FIRST, then paginate, to ensure correct offset calculation
		// Otherwise, offset would be wrong if some events are filtered out
		eventsToPaginate := make([]Event, 0, len(events))
		for _, event := range events {
			if shouldReturnEvent(event, opts.IncludeStreaming) {
				eventsToPaginate = append(eventsToPaginate, event)
			}
		}

		// Now paginate the filtered events
		start := opts.Offset
		end := opts.Offset + opts.Limit

		if start < 0 {
			start = 0
		}
		if end > len(eventsToPaginate) {
			end = len(eventsToPaginate)
		}

		if start < end {
			// Get events from start (oldest first)
			result = eventsToPaginate[start:end]
		} else {
			result = []Event{}
		}
		// Offset pagination is used by the Conversation view to prepend older
		// history. Report whether another page really exists; guessing from a
		// full page makes the final "Load earlier" action appear forever when
		// the total happens to be an exact multiple of the page size.
		hasMore = end < len(eventsToPaginate)
		// For pagination, lastProcessedIndex is not relevant (offset-based, not index-based)
		lastProcessedIndex = -1
	} else {
		// No specific mode: return all filtered events
		filtered := make([]Event, 0, len(events))
		for _, event := range events {
			if shouldReturnEvent(event, opts.IncludeStreaming) {
				filtered = append(filtered, event)
			}
		}
		result = filtered
		// For "all events" mode, we processed all events
		// Add baseIndex to return absolute index
		lastProcessedIndex = baseIndex + len(events) - 1
	}

	return GetEventsResult{
		Events:             result,
		Exists:             true,
		TotalCount:         len(events),
		LastProcessedIndex: lastProcessedIndex,
		HasMore:            hasMore,
	}
}

// GetTerminalEvents retrieves only events assigned to terminalID. New events
// use the write-time terminal index; the fallback scan keeps restored legacy
// events readable while old history ages out.
func (es *EventStore) GetTerminalEvents(sessionID, terminalID string, opts GetTerminalEventsOptions) GetTerminalEventsResult {
	es.hydrateSession(sessionID)
	es.mu.RLock()
	defer es.mu.RUnlock()

	sessionID = strings.TrimSpace(sessionID)
	terminalID = strings.TrimSpace(terminalID)
	if sessionID == "" || terminalID == "" {
		return GetTerminalEventsResult{Events: []Event{}}
	}

	owned := es.terminalEvents[sessionID][terminalID]
	if len(owned) == 0 {
		// Compatibility for events restored before TerminalID was materialized.
		for _, event := range es.events[sessionID] {
			resolvedID := event.TerminalID
			if resolvedID == "" {
				ownerID := ResolveTerminalOwnerID(sessionID, event, nil)
				resolvedID = TerminalIDForOwner(sessionID, ownerID)
			}
			if resolvedID == terminalID {
				owned = append(owned, event)
			}
		}
	}

	filtered := make([]Event, 0, len(owned))
	for _, event := range owned {
		if shouldReturnEvent(event, false) {
			filtered = append(filtered, event)
		}
	}
	if len(filtered) == 0 {
		_, sessionExists := es.events[sessionID]
		return GetTerminalEventsResult{Events: []Event{}, Exists: sessionExists}
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = InitialEventsLimit
	}
	if limit > MaxPollingLimit {
		limit = MaxPollingLimit
	}

	start, end := 0, len(filtered)
	switch {
	case opts.AfterSequence > 0:
		start = len(filtered)
		for i, event := range filtered {
			if event.Sequence > opts.AfterSequence {
				start = i
				break
			}
		}
		end = start + limit
		if end > len(filtered) {
			end = len(filtered)
		}
	case opts.BeforeSequence > 0:
		end = 0
		for i, event := range filtered {
			if event.Sequence >= opts.BeforeSequence {
				end = i
				break
			}
			end = i + 1
		}
		start = end - limit
		if start < 0 {
			start = 0
		}
	default:
		start = len(filtered) - limit
		if start < 0 {
			start = 0
		}
	}

	page := append([]Event(nil), filtered[start:end]...)
	result := GetTerminalEventsResult{
		Events:   page,
		Exists:   true,
		HasOlder: start > 0,
		HasNewer: end < len(filtered),
	}
	if len(page) > 0 {
		result.OldestSequence = page[0].Sequence
		result.LatestSequence = page[len(page)-1].Sequence
	}
	return result
}

// GetSessionStatus returns the status of a session
func (es *EventStore) GetSessionStatus(sessionID string) (int, bool) {
	es.mu.RLock()
	defer es.mu.RUnlock()

	events, exists := es.events[sessionID]
	if !exists {
		return 0, false
	}

	return len(events), true
}

// GetLatestEventIndex returns the absolute cursor for the newest retained
// event without serializing the retained event payloads.
func (es *EventStore) GetLatestEventIndex(sessionID string) (int, bool) {
	es.hydrateSession(sessionID)
	es.mu.RLock()
	defer es.mu.RUnlock()

	sessionEvents, exists := es.events[sessionID]
	if !exists {
		return -1, false
	}
	return es.sessionStartIndices[sessionID] + len(sessionEvents) - 1, true
}

// RemoveSession removes a session and its events
func (es *EventStore) RemoveSession(sessionID string) {
	es.mu.Lock()
	defer es.mu.Unlock()

	delete(es.events, sessionID)
	delete(es.terminalEvents, sessionID)
	delete(es.nextSequence, sessionID)
	delete(es.sessionStartIndices, sessionID)
	delete(es.sessionOwners, sessionID)
	delete(es.persistenceClasses, sessionID)
	delete(es.journalProbed, sessionID)
	// Explicit removal is an in-process tombstone. A later read or new event
	// must not resurrect the old durable tail; a new process may restore it.
	es.hydratedSessions[sessionID] = true
}

// GetActiveSessions returns all active session IDs
func (es *EventStore) GetActiveSessions() []string {
	es.mu.RLock()
	defer es.mu.RUnlock()

	sessions := make([]string, 0, len(es.events))
	for sessionID := range es.events {
		sessions = append(sessions, sessionID)
	}

	return sessions
}

// cleanupRoutine periodically cleans up inactive observers
func (es *EventStore) cleanupRoutine() {
	for {
		select {
		case <-es.cleanupTicker.C:
			es.cleanupInactiveSessions()
		case <-es.stopCh:
			es.cleanupTicker.Stop()
			return
		}
	}
}

// cleanupInactiveSessions removes sessions that haven't been active recently
func (es *EventStore) cleanupInactiveSessions() {
	// For now, we'll implement a simple cleanup based on event count
	// In a real implementation, you might track last activity time
	es.mu.Lock()
	defer es.mu.Unlock()

	for sessionID, events := range es.events {
		// Remove sessions with no events (inactive)
		if len(events) == 0 {
			delete(es.events, sessionID)
			delete(es.terminalEvents, sessionID)
			delete(es.nextSequence, sessionID)
		}
	}
}

// Stop stops the event store and cleanup routine
func (es *EventStore) Stop() {
	close(es.stopCh)
	es.hydrateMu.Lock()
	defer es.hydrateMu.Unlock()
	es.mu.Lock()
	journal := es.durableJournal
	es.durableJournal = nil
	es.mu.Unlock()
	if journal != nil {
		_ = journal.Close()
	}
}

// GetStats returns statistics about the event store
func (es *EventStore) GetStats() map[string]interface{} {
	es.mu.RLock()
	defer es.mu.RUnlock()

	totalEvents := 0
	for _, events := range es.events {
		totalEvents += len(events)
	}

	return map[string]interface{}{
		"total_sessions": len(es.events),
		"total_events":   totalEvents,
		"max_events":     es.maxEvents,
	}
}

// DelegationStartEventData implements events.EventData for delegation_start
type DelegationStartEventData struct {
	DelegationID      string   `json:"delegation_id"`
	Depth             int      `json:"depth"`
	Instruction       string   `json:"instruction"`
	ReasoningLevel    string   `json:"reasoning_level,omitempty"`
	ModelID           string   `json:"model_id,omitempty"`
	Servers           []string `json:"servers,omitempty"`
	BackgroundAgentID string   `json:"background_agent_id,omitempty"`
	AgentTemplate     string   `json:"agent_template,omitempty"`
	Timestamp         string   `json:"timestamp"`
}

func (d *DelegationStartEventData) GetEventType() events.EventType {
	return events.EventType("delegation_start")
}

// DelegationEndEventData implements events.EventData for delegation_end
type DelegationEndEventData struct {
	DelegationID string  `json:"delegation_id"`
	Depth        int     `json:"depth"`
	Result       string  `json:"result"`
	Error        string  `json:"error,omitempty"`
	Success      bool    `json:"success"`
	Timestamp    string  `json:"timestamp"`
	InputTokens  int64   `json:"input_tokens,omitempty"`
	OutputTokens int64   `json:"output_tokens,omitempty"`
	ToolCalls    int64   `json:"tool_calls,omitempty"`
	Duration     string  `json:"duration,omitempty"`
	TotalCostUSD float64 `json:"total_cost_usd,omitempty"`
}

func (d *DelegationEndEventData) GetEventType() events.EventType {
	return events.EventType("delegation_end")
}

// GenericEventData is a generic event data type that wraps a map
// Used for events that don't need a specific struct (e.g. background agent events)
type GenericEventData struct {
	EventType string                 `json:"event_type"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
}

func (d *GenericEventData) GetEventType() events.EventType {
	return events.EventType(d.EventType)
}

// MarshalJSON flattens Fields into the top-level JSON
func (d *GenericEventData) MarshalJSON() ([]byte, error) {
	result := make(map[string]interface{})
	for k, v := range d.Fields {
		result[k] = v
	}
	result["event_type"] = d.EventType
	return json.Marshal(result)
}

// NewGenericEventData creates a GenericEventData with the given type and fields
func NewGenericEventData(eventType string, fields map[string]interface{}) *GenericEventData {
	return &GenericEventData{
		EventType: eventType,
		Fields:    fields,
	}
}

// --- streaming telemetry -----------------------------------------------------
//
// Streaming defects are invisible in the UI alone: "no text appeared" can mean
// the provider never streamed, or it streamed only raw terminal frames the
// product surface deliberately suppresses, or it streamed one block at the very
// end. Those are three different bugs with three different owners, and telling
// them apart from a screenshot is guesswork -- diagnosing the codex case cost a
// long round of browser probing that one log line would have answered.
//
// Every streaming_chunk passes through AddEvent, so this is the one place that
// sees the true shape. Per chunk we log the two fields that decide how a UI may
// render it (source, is_delta); per turn we log the summary that answers "did
// this turn stream at all, and how did it arrive".

type streamingTurnStats struct {
	chunks    int
	deltas    int
	terminal  int
	clean     int
	chars     int
	firstAt   time.Time
	lastAt    time.Time
	startedAt time.Time
}

var (
	streamingStatsMu sync.Mutex
	streamingStats   = map[string]*streamingTurnStats{}
)

// streamingChunkFields pulls source/is_delta/content out of whichever envelope
// shape the chunk arrived in (typed event or generic map).
func streamingChunkFields(event Event) (source string, isDelta bool, contentLen int) {
	if event.Data == nil {
		return "", false, 0
	}
	switch d := event.Data.Data.(type) {
	case *events.StreamingChunkEvent:
		return d.Source, d.IsDelta, len(d.Content)
	case *events.GenericEventData:
		source, _ = d.Data["source"].(string)
		isDelta, _ = d.Data["is_delta"].(bool)
		if content, ok := d.Data["content"].(string); ok {
			contentLen = len(content)
		}
		return source, isDelta, contentLen
	}
	return "", false, 0
}

func logStreamingChunkTelemetry(sessionID string, event Event) {
	switch event.Type {
	case "streaming_start":
		streamingStatsMu.Lock()
		streamingStats[sessionID] = &streamingTurnStats{startedAt: time.Now()}
		streamingStatsMu.Unlock()
		log.Printf("[STREAM] session=%s turn started", sessionID)
	case "streaming_chunk":
		source, isDelta, contentLen := streamingChunkFields(event)
		if source == "" {
			source = "unset"
		}
		streamingStatsMu.Lock()
		stats := streamingStats[sessionID]
		if stats == nil {
			stats = &streamingTurnStats{startedAt: time.Now()}
			streamingStats[sessionID] = stats
		}
		now := time.Now()
		if stats.firstAt.IsZero() {
			stats.firstAt = now
		}
		stats.lastAt = now
		stats.chunks++
		stats.chars += contentLen
		if isDelta {
			stats.deltas++
		}
		if source == "terminal" {
			stats.terminal++
		} else {
			stats.clean++
		}
		index := stats.chunks
		sinceStart := now.Sub(stats.startedAt)
		streamingStatsMu.Unlock()
		log.Printf("[STREAM] session=%s chunk=%d source=%s is_delta=%t chars=%d t+%dms",
			sessionID, index, source, isDelta, contentLen, sinceStart.Milliseconds())
	case "streaming_end":
		streamingStatsMu.Lock()
		stats := streamingStats[sessionID]
		delete(streamingStats, sessionID)
		streamingStatsMu.Unlock()
		if stats == nil {
			log.Printf("[STREAM] session=%s turn ended with NO streaming chunks", sessionID)
			return
		}
		var firstMS, spreadMS int64
		if !stats.firstAt.IsZero() {
			firstMS = stats.firstAt.Sub(stats.startedAt).Milliseconds()
			spreadMS = stats.lastAt.Sub(stats.firstAt).Milliseconds()
		}
		// spread_ms is the tell: a turn whose chunks all land within a few ms
		// did not stream, it just delivered late, however many chunks it had.
		log.Printf("[STREAM] session=%s turn ended chunks=%d clean=%d terminal=%d deltas=%d chars=%d first_chunk_ms=%d spread_ms=%d",
			sessionID, stats.chunks, stats.clean, stats.terminal, stats.deltas, stats.chars, firstMS, spreadMS)
	}
}

// --- tool-call telemetry -----------------------------------------------------
//
// A tool chip that spins forever means its ToolCallEnd never arrived, or arrived
// under a different ToolCallID than the Start. Both look identical in the UI and
// neither is visible in the frontend's own logs, so the pairing is logged here
// where both events are guaranteed to pass. The P0 streaming e2e asserts only
// that a ToolCallStart happened -- it never checks that a matching End follows --
// so an unpaired call is exactly the defect our tests cannot currently catch.

type toolCallTelemetry struct {
	name      string
	startedAt time.Time
}

var (
	toolCallsMu  sync.Mutex
	openToolCall = map[string]map[string]*toolCallTelemetry{} // sessionID -> toolCallID
	// toolResultResolver, when set, recovers a tool call's real output and
	// runtime from the provider's own record. Injected rather than imported so
	// this package stays free of provider knowledge; cmd/server owns the
	// Claude Code implementation (PLAT-141).
	toolResultResolver   ToolResultResolver
	toolResultResolverMu sync.RWMutex

	// settleInFlight stops one turn from scheduling two settle passes. A turn
	// emits BOTH agent_end and unified_completion, so the handler started a
	// goroutine for each. The event stream stayed correct — the first pass
	// removed the calls it settled, so the second found none and emitted
	// nothing — but the LOG became unreadable: at 21:23:23 the same session
	// reported "5 of 5 tool call(s) never reported after 5s -- settling" and
	// "all 5 open tool call(s) reported within the grace window" in the same
	// second. Diagnosing this bug from those two lines is impossible, which is
	// the whole reason the logging exists.
	settleInFlight = map[string]bool{}
)

func (es *EventStore) logToolCallTelemetry(sessionID string, event Event) {
	if event.Data == nil {
		return
	}
	switch d := event.Data.Data.(type) {
	case *events.ToolCallStartEvent:
		toolCallsMu.Lock()
		if openToolCall[sessionID] == nil {
			openToolCall[sessionID] = map[string]*toolCallTelemetry{}
		}
		openToolCall[sessionID][d.ToolCallID] = &toolCallTelemetry{name: d.ToolName, startedAt: time.Now()}
		open := len(openToolCall[sessionID])
		toolCallsMu.Unlock()
		log.Printf("[TOOL] session=%s START id=%s name=%s args_len=%d open=%d",
			sessionID, d.ToolCallID, d.ToolName, len(d.ToolParams.Arguments), open)
	case *events.ToolCallEndEvent:
		toolCallsMu.Lock()
		started := openToolCall[sessionID][d.ToolCallID]
		if started != nil {
			delete(openToolCall[sessionID], d.ToolCallID)
		}
		open := len(openToolCall[sessionID])
		toolCallsMu.Unlock()
		if started == nil {
			// The chip keyed by the Start id will never clear.
			log.Printf("[TOOL] session=%s END id=%s name=%s UNPAIRED (no matching START id) result_len=%d open=%d",
				sessionID, d.ToolCallID, d.ToolName, len(d.Result), open)
			return
		}
		log.Printf("[TOOL] session=%s END id=%s name=%s duration=%s result_len=%d open=%d",
			sessionID, d.ToolCallID, started.name, time.Since(started.startedAt).Truncate(time.Millisecond), len(d.Result), open)
	case *events.ToolCallErrorEvent:
		// A tool call that fails still REPORTED — with a real error, not
		// silence. Before this case existed, only ToolCallEndEvent cleared an
		// entry, so an errored call stayed in openToolCall forever and, at
		// turn end, was indistinguishable from a call that got no response at
		// all: settleOpenToolCalls swept it into the same synthetic-settle
		// bucket and the UI showed "no result reported" for a call that had
		// in fact reported a specific, real error.
		//
		// Confirmed live 2026-08-20 on a pi-cli session: two real errors
		// ("Working directory does not exist", then an ACCESS DENIED write to
		// the wrong scoped folder) both left their ToolCallID stuck open, and
		// both later turned up inside a "N of N tool call(s) produced no end
		// event" settle alongside genuinely unreported calls.
		toolCallsMu.Lock()
		started := openToolCall[sessionID][d.ToolCallID]
		if started != nil {
			delete(openToolCall[sessionID], d.ToolCallID)
		}
		open := len(openToolCall[sessionID])
		toolCallsMu.Unlock()
		if started == nil {
			log.Printf("[TOOL] session=%s ERROR id=%s name=%s UNPAIRED (no matching START id) error_len=%d open=%d",
				sessionID, d.ToolCallID, d.ToolName, len(d.Error), open)
			return
		}
		log.Printf("[TOOL] session=%s ERROR id=%s name=%s duration=%s error_len=%d open=%d",
			sessionID, d.ToolCallID, started.name, time.Since(started.startedAt).Truncate(time.Millisecond), len(d.Error), open)
	}
	// A turn that ends with calls still open is the spinning-chip case.
	if event.Type == "agent_end" || event.Type == "unified_completion" {
		toolCallsMu.Lock()
		pending := make(map[string]*toolCallTelemetry, len(openToolCall[sessionID]))
		for id, tc := range openToolCall[sessionID] {
			pending[id] = tc
		}
		toolCallsMu.Unlock()
		toolCallsMu.Lock()
		alreadySettling := settleInFlight[sessionID]
		if len(pending) > 0 && !alreadySettling {
			settleInFlight[sessionID] = true
		}
		toolCallsMu.Unlock()
		if alreadySettling {
			return
		}
		if len(pending) > 0 {
			names := make([]string, 0, len(pending))
			for id, tc := range pending {
				names = append(names, tc.name+"("+id+")")
			}
			sort.Strings(names)
			log.Printf("[TOOL] session=%s turn ended with %d tool call(s) open -- waiting %s for late results: %s",
				sessionID, len(pending), toolCallSettleGrace, strings.Join(names, ", "))
			go es.settleAfterGrace(sessionID, event, pending)
		} else {
			toolCallsMu.Lock()
			delete(openToolCall, sessionID)
			toolCallsMu.Unlock()
		}
	}
}

// toolCallSettleGrace is how long a turn-end waits for tool results that are
// already on their way.
//
// The turn-end signal and the tool-end events reach this store from different
// places — the CLI's own completion versus the bridge callback that runs the
// tool — and they are not ordered against each other. Measured on
// tectonicusadaytrading 2026-08-18: the turn ended at 20:34:28 and the tool's
// completion was processed at 20:34:29. Settling the instant the turn ends
// therefore condemns calls that are one second from landing, which is how
// fourteen completed shell commands were reported as never having reported.
//
// The native transcript for that session shows 215 tool_use and 215
// tool_result: the model received everything. Nothing is lost here except the
// UI's copy, so the grace window only needs to cover the gap between two
// pipelines, not any real tool latency.
var toolCallSettleGrace = 5 * time.Second

// A settled call carries no message at all.
//
// Three versions of a message were tried here — the mechanism, then the
// evidence, then a short placeholder — and every one of them put a diagnostic
// in front of someone reading a chat. The gap is ours: the tool ran, the agent
// used its result, and only our copy is missing (PLAT-141 measures one such
// call completing in 41ms). None of that is the reader's problem to interpret,
// so it belongs in the server log, where it now is, and nowhere else.
//
// The event is still emitted, because without an end the chip spins forever —
// which is the bug this whole line of work started from.

// settleAfterGrace closes only the calls that are still missing once late
// results have had time to arrive.
// ToolResultResolver recovers a tool call the live stream never completed.
// name and startedAt are the caller's own record of the orphaned call, passed
// through so a resolver correlating against a different id space (PLAT-143)
// has what it needs to match on. Returns ok=false when nothing has a record
// of it either.
type ToolResultResolver func(sessionID, toolCallID, name string, startedAt time.Time) (result string, duration time.Duration, ok bool)

// SetToolResultResolver installs the recovery path used when a tool call
// produces no end event.
func SetToolResultResolver(resolver ToolResultResolver) {
	toolResultResolverMu.Lock()
	toolResultResolver = resolver
	toolResultResolverMu.Unlock()
}

func resolveToolResult(sessionID, toolCallID, name string, startedAt time.Time) (string, time.Duration, bool) {
	toolResultResolverMu.RLock()
	resolver := toolResultResolver
	toolResultResolverMu.RUnlock()
	if resolver == nil {
		return "", 0, false
	}
	return resolver(sessionID, toolCallID, name, startedAt)
}

func (es *EventStore) settleAfterGrace(sessionID string, turnEnd Event, pending map[string]*toolCallTelemetry) {
	time.Sleep(toolCallSettleGrace)

	toolCallsMu.Lock()
	delete(settleInFlight, sessionID)
	stuck := make(map[string]*toolCallTelemetry, len(pending))
	for id, tc := range pending {
		// Still open means the end never arrived; anything that landed during
		// the grace window removed itself from the map on the way through.
		if _, open := openToolCall[sessionID][id]; open {
			stuck[id] = tc
			delete(openToolCall[sessionID], id)
		}
	}
	if len(openToolCall[sessionID]) == 0 {
		delete(openToolCall, sessionID)
	}
	toolCallsMu.Unlock()

	if len(stuck) == 0 {
		log.Printf("[TOOL] session=%s all %d open tool call(s) reported within the grace window", sessionID, len(pending))
		return
	}
	names := make([]string, 0, len(stuck))
	for id, tc := range stuck {
		names = append(names, tc.name+"("+id+")")
	}
	sort.Strings(names)
	// The whole diagnostic lives here rather than on screen. PLAT-141: these
	// calls complete promptly — one measured at 41ms — and their results simply
	// never become events, so the durations below are open-to-settle, not tool
	// runtime, and should not be read as slow tools.
	log.Printf("[TOOL] session=%s PLAT-141: %d of %d tool call(s) produced no end event within %s; closing their UI chips with no output. "+
		"open-to-settle (NOT tool runtime): %s",
		sessionID, len(stuck), len(pending), toolCallSettleGrace, strings.Join(names, ", "))
	es.settleOpenToolCalls(sessionID, turnEnd, stuck)
}

// settleOpenToolCalls closes chips whose tool call never reported an end.
//
// The condition was already detected here and logged verbatim as "their UI
// chips will spin forever" — accurately, and then nothing was done about it. A
// user watching a chat sees a spinner that outlives the turn and reasonably
// concludes the agent is still working; on salesoutreach that produced a chat
// that looked busy for as long as it stayed open, while the session had
// genuinely completed.
//
// The synthetic end is deliberately marked as such rather than dressed up as a
// normal completion. The tool really did not report a result, and a chip that
// silently turns green would replace a visible wrong state with an invisible
// one — the same trade this codebase refuses elsewhere.
//
// Safe to call from AddEvent: the store mutex is already released by this
// point, and the events emitted are ToolCallEnd, which cannot re-enter this
// branch.
func (es *EventStore) settleOpenToolCalls(sessionID string, turnEnd Event, stuck map[string]*toolCallTelemetry) {
	if es == nil {
		return
	}
	for id, tc := range stuck {
		now := time.Now()
		name := tc.name
		if name == "" {
			name = "unknown_tool"
		}
		// The provider's own record is complete where ours is not, so recover
		// the real output and the real runtime rather than closing the chip
		// blank (PLAT-141). Falls through to an empty settle when the provider
		// has no record either.
		result := ""
		duration := now.Sub(tc.startedAt)
		// displayTimestamp defaults to the settle moment -- the only signal
		// available when nothing is recovered. When a real duration IS
		// recovered, the settle moment is not this call's completion time; it
		// is whenever the batch happened to run, which is why several settled
		// calls with different real durations could all display the identical
		// clock time. Use the call's own real start+duration instead.
		displayTimestamp := now
		if recovered, realDuration, ok := resolveToolResult(sessionID, id, name, tc.startedAt); ok {
			result = recovered
			if realDuration > 0 {
				duration = realDuration
				displayTimestamp = tc.startedAt.Add(realDuration)
			}
			log.Printf("[TOOL] session=%s PLAT-141: recovered %s(%s) from the provider transcript — %d bytes, real runtime %s",
				sessionID, name, id, len(recovered), realDuration.Round(time.Millisecond))
		}
		// A completion, not an error. The tool ran — measured on
		// tectonicusadaytrading, whose native transcript holds 215 tool_use and
		// 215 tool_result — and the agent received its result. Only our copy is
		// missing. Painting that red puts a failure badge on a run where nothing
		// failed, which is what an earlier version of this did and what made it
		// worth reporting twice.
		//
		// The output field says the output was not captured rather than showing
		// a fabricated one, so nothing is asserted that was not observed.
		es.AddEvent(sessionID, Event{
			ID:                fmt.Sprintf("settle-%s-%d", id, now.UnixNano()),
			Type:              "tool_call_end",
			Timestamp:         displayTimestamp,
			SessionID:         sessionID,
			ExecutionID:       turnEnd.ExecutionID,
			ParentExecutionID: turnEnd.ParentExecutionID,
			ExecutionKind:     turnEnd.ExecutionKind,
			TerminalOwnerID:   turnEnd.TerminalOwnerID,
			Data: &events.AgentEvent{
				Type:      events.EventType("tool_call_end"),
				Timestamp: displayTimestamp,
				SessionID: sessionID,
				Data: &events.ToolCallEndEvent{
					ToolName:   name,
					ToolCallID: id,
					Duration:   duration,
					Result:     result,
					// Duration here is open-to-settle, not tool runtime (see the
					// log line above, which says so verbatim). Flagging it lets a
					// consumer label it honestly instead of presenting it as a
					// measured runtime it is not.
					SyntheticSettle: true,
				},
			},
		})
	}
}

// EventStoreStats summarises what the store holds in memory.
type EventStoreStats struct {
	Sessions       int
	Events         int
	TerminalEvents int
	// Largest lists the sessions holding the most events, largest first.
	Largest []SessionEventCount
}

// SessionEventCount is one session's in-memory event count.
type SessionEventCount struct {
	SessionID      string
	Events         int
	TerminalEvents int
}

// Stats reports in-memory sizes, for the periodic memory log.
func (es *EventStore) Stats(top int) EventStoreStats {
	es.mu.RLock()
	defer es.mu.RUnlock()
	counts := map[string]*SessionEventCount{}
	count := func(sessionID string) *SessionEventCount {
		entry := counts[sessionID]
		if entry == nil {
			entry = &SessionEventCount{SessionID: sessionID}
			counts[sessionID] = entry
		}
		return entry
	}
	stats := EventStoreStats{}
	for sessionID, events := range es.events {
		count(sessionID).Events = len(events)
		stats.Events += len(events)
	}
	for sessionID, terminals := range es.terminalEvents {
		for _, events := range terminals {
			count(sessionID).TerminalEvents += len(events)
			stats.TerminalEvents += len(events)
		}
	}
	stats.Sessions = len(counts)
	for _, entry := range counts {
		stats.Largest = append(stats.Largest, *entry)
	}
	sort.Slice(stats.Largest, func(i, j int) bool {
		return stats.Largest[i].Events+stats.Largest[i].TerminalEvents > stats.Largest[j].Events+stats.Largest[j].TerminalEvents
	})
	if len(stats.Largest) > top {
		stats.Largest = stats.Largest[:top]
	}
	return stats
}
