package events

import (
	"time"
)

// Unified EventType enum combining all event types
type EventType string

// Agent Event Types (from mcpagent/events.go)
const (
	// Conversation events
	ConversationStart    EventType = "conversation_start"
	ConversationEnd      EventType = "conversation_end"
	ConversationError    EventType = "conversation_error"
	ConversationTurn     EventType = "conversation_turn"
	ConversationThinking EventType = "conversation_thinking"

	// LLM events
	LLMGenerationStart EventType = "llm_generation_start"
	LLMGenerationEnd   EventType = "llm_generation_end"
	LLMGenerationError EventType = "llm_generation_error"

	// Tool events
	ToolCallStart EventType = "tool_call_start"
	ToolCallEnd   EventType = "tool_call_end"
	ToolCallError EventType = "tool_call_error"

	// Agent events
	AgentStart EventType = "agent_start"
	AgentEnd   EventType = "agent_end"
	AgentError EventType = "agent_error"

	// System events
	SystemPrompt EventType = "system_prompt"
	UserMessage  EventType = "user_message"

	// Delivery receipt events: two-stage confirmation for live input
	// into retained coding CLIs. The HTTP ack is the fast pane
	// confirmation (single tick); this event is the durable
	// rollout/transcript proof (double tick), matched to the user
	// message row by metadata.message_id.
	LiveInputConfirmed EventType = "live_input_confirmed"
	// Muse task lifecycle continues after its foreground run completes.
	CodingAgentBackgroundTask EventType = "coding_agent_background_task"
	CodingAgentQuestion       EventType = "coding_agent_question"
	NativeSubagentTranscript  EventType = "native_subagent_transcript"

	// Streaming events
	StreamingStart      EventType = "streaming_start"
	StreamingChunk      EventType = "streaming_chunk"
	StreamingEnd        EventType = "streaming_end"
	StreamingStatusLine EventType = "status_line"

	// Debug events
	TokenUsage EventType = "token_usage"

	// Error and model events
	MaxTurnsReached  EventType = "max_turns_reached"
	ContextCancelled EventType = "context_cancelled"

	// MCP server events
	// NOTE: MCPServerConnection is the nominal type of a payload-carrier
	// struct; live code always re-types it to ConnectionStart/End before
	// emit, so the bare wire type is never produced. Batch 4 removed the
	// bare name from schemas/generated/UI; the const stays as the payload
	// tag for GetEventType.
	MCPServerConnection      EventType = "mcp_server_connection"
	MCPServerSelection       EventType = "mcp_server_selection"
	MCPServerConnectionStart EventType = "mcp_server_connection_start"
	MCPServerConnectionEnd   EventType = "mcp_server_connection_end"

	// Cache events: only the generic carrier is ever emitted (all cache
	// constructors return this type); the per-operation wire strings were
	// removed on 2026-09-22 (no emitters, no readers).
	GenericCache EventType = "cache_event"

	// Tool execution events
	ToolExecution          EventType = "tool_execution"
	LLMGenerationWithRetry EventType = "llm_generation_with_retry"

	// Additional event types from mcpagent
	RetryAttempt EventType = "retry_attempt"
	BrokenPipe   EventType = "broken_pipe"

	// Unified completion event
	EventTypeUnifiedCompletion EventType = "unified_completion"
)

// Orchestrator Event Types (from orchestrator/events/events.go)
const (
	// Orchestrator events (only End is emitted; Start/Error never were)
	OrchestratorEnd EventType = "orchestrator_end"

	// Orchestrator Agent lifecycle events
	OrchestratorAgentStart EventType = "orchestrator_agent_start"
	OrchestratorAgentEnd   EventType = "orchestrator_agent_end"
	OrchestratorAgentError EventType = "orchestrator_agent_error"

	// Todo planning events
	VariablesExtracted EventType = "variables_extracted"

	// Human Verification events
	RequestHumanFeedback  EventType = "request_human_feedback"
	BlockingHumanFeedback EventType = "blocking_human_feedback"

	// Step token usage event
	StepTokenUsage EventType = "step_token_usage"

	// Pre-validation events
	PreValidationCompleted EventType = "pre_validation_completed"
)

// Unified Event structure with hierarchy support
type Event struct {
	Type          EventType              `json:"type"`
	Timestamp     time.Time              `json:"timestamp"`
	TraceID       string                 `json:"trace_id,omitempty"`
	SpanID        string                 `json:"span_id,omitempty"`
	ParentID      string                 `json:"parent_id,omitempty"` // NEW: Parent event ID
	CorrelationID string                 `json:"correlation_id,omitempty"`
	Data          EventData              `json:"data"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`

	// NEW: Hierarchy fields
	HierarchyLevel int       `json:"hierarchy_level"`       // 0=root, 1=child, 2=grandchild
	ParentType     EventType `json:"parent_type,omitempty"` // Type of parent event
	SessionID      string    `json:"session_id,omitempty"`  // Group related events
	Component      string    `json:"component,omitempty"`   // orchestrator, agent, llm, tool
	Query          string    `json:"query,omitempty"`       // Store the actual query
}

// EventData interface for all event data types
type EventData interface {
	GetEventType() EventType
}

// Base event data structure
type BaseEventData struct {
	Timestamp      time.Time              `json:"timestamp"`
	TraceID        string                 `json:"trace_id,omitempty"`
	SpanID         string                 `json:"span_id,omitempty"`
	EventID        string                 `json:"event_id,omitempty"`
	ParentID       string                 `json:"parent_id,omitempty"`
	IsEndEvent     bool                   `json:"is_end_event,omitempty"`
	CorrelationID  string                 `json:"correlation_id,omitempty"` // Links start/end event pairs
	HierarchyLevel int                    `json:"hierarchy_level"`          // 0=root, 1=child, 2=grandchild
	SessionID      string                 `json:"session_id,omitempty"`     // Group related events
	Component      string                 `json:"component,omitempty"`      // orchestrator, agent, llm, tool
	Metadata       map[string]interface{} `json:"metadata,omitempty"`       // Additional context data
}

// SetHierarchyFields sets the hierarchy-related fields on BaseEventData
func (b *BaseEventData) SetHierarchyFields(parentID string, level int, sessionID string, component string) {
	b.ParentID = parentID
	b.HierarchyLevel = level
	b.SessionID = sessionID
	b.Component = component
}

// GetBaseEventData returns a pointer to the BaseEventData for hierarchy field setting
func (b *BaseEventData) GetBaseEventData() *BaseEventData {
	return b
}

// Helper function to get component from event type
func GetComponentFromEventType(eventType EventType) string {
	switch eventType {
	case VariablesExtracted,
		StepTokenUsage,
		RequestHumanFeedback, BlockingHumanFeedback,
		PreValidationCompleted:
		return "orchestrator"
	case AgentStart, AgentEnd, AgentError:
		return "agent"
	case LLMGenerationStart, LLMGenerationEnd, LLMGenerationError:
		return "llm"
	case ToolCallStart, ToolCallEnd, ToolCallError:
		return "tool"
	case ConversationStart, ConversationEnd, ConversationError, ConversationTurn, ConversationThinking:
		return "conversation"
	case SystemPrompt, UserMessage:
		return "system"
	default:
		return "system"
	}
}

// Helper function to check if event is a start event
func IsStartEvent(eventType EventType) bool {
	return eventType == ConversationStart ||
		eventType == ConversationTurn ||
		eventType == LLMGenerationStart ||
		eventType == ToolCallStart ||
		eventType == AgentStart
}

// Helper function to check if event is an end event
func IsEndEvent(eventType EventType) bool {
	return eventType == ConversationEnd ||
		eventType == LLMGenerationEnd ||
		eventType == ToolCallEnd ||
		eventType == AgentEnd
}
