package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	orchestrator_events "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/mcpagent/events"

	"github.com/invopop/jsonschema"
)

// =============================================================================
// SECTION 1: Wire Format Types (match actual backend → frontend format)
// =============================================================================

// PollingEventActual matches the actual Event struct from event_store.go
// This is what the backend sends to the frontend over the wire
type PollingEventActual struct {
	ID                string               `json:"id" jsonschema:"description=Unique event identifier"`
	Type              events.EventType     `json:"type" jsonschema:"description=Event type discriminator"`
	Timestamp         time.Time            `json:"timestamp" jsonschema:"description=Event timestamp"`
	SessionID         string               `json:"session_id,omitempty" jsonschema:"description=Session identifier"`
	ExecutionID       string               `json:"execution_id,omitempty" jsonschema:"description=Canonical execution identifier"`
	ParentExecutionID string               `json:"parent_execution_id,omitempty" jsonschema:"description=Parent execution identifier"`
	ExecutionKind     string               `json:"execution_kind,omitempty" jsonschema:"description=Execution kind"`
	TerminalOwnerID   string               `json:"terminal_owner_id,omitempty" jsonschema:"description=Canonical owner identifier for one terminal transcript"`
	TerminalID        string               `json:"terminal_id,omitempty" jsonschema:"description=Canonical terminal transcript identifier"`
	Sequence          int64                `json:"sequence,omitempty" jsonschema:"description=Stable session event sequence cursor"`
	Error             string               `json:"error,omitempty" jsonschema:"description=Error message if any"`
	Data              *AgentEventForSchema `json:"data,omitempty" jsonschema:"description=The AgentEvent containing event details"`
}

// AgentEventForSchema matches AgentEvent from data.go - this is the wrapper around actual event data
type AgentEventForSchema struct {
	Type           events.EventType `json:"type" jsonschema:"description=Event type (same as parent)"`
	Timestamp      time.Time        `json:"timestamp" jsonschema:"description=Event timestamp"`
	EventIndex     int              `json:"event_index" jsonschema:"description=Sequential event index"`
	TraceID        string           `json:"trace_id,omitempty" jsonschema:"description=Trace ID for distributed tracing"`
	SpanID         string           `json:"span_id,omitempty" jsonschema:"description=Span ID for distributed tracing"`
	ParentID       string           `json:"parent_id,omitempty" jsonschema:"description=Parent event ID"`
	CorrelationID  string           `json:"correlation_id,omitempty" jsonschema:"description=Links start/end event pairs"`
	HierarchyLevel int              `json:"hierarchy_level" jsonschema:"description=0=root, 1=child, 2=grandchild"`
	SessionID      string           `json:"session_id,omitempty" jsonschema:"description=Group related events"`
	Component      string           `json:"component,omitempty" jsonschema:"description=orchestrator, agent, llm, tool"`
	Data           EventDataUnion   `json:"data" jsonschema:"description=The actual typed event data"`
}

// =============================================================================
// SECTION 2: EventDataUnion - All possible event data types
// This is the union of all typed event data
// =============================================================================

// EventDataUnion contains all possible event data types as optional fields
// The frontend uses event.data.type to determine which field is populated
type EventDataUnion struct {
	// Core Agent Events
	AgentStart *events.AgentStartEvent `json:"agent_start,omitempty"`
	AgentEnd   *events.AgentEndEvent   `json:"agent_end,omitempty"`
	AgentError *events.AgentErrorEvent `json:"agent_error,omitempty"`

	// Conversation Events
	ConversationStart *events.ConversationStartEvent `json:"conversation_start,omitempty"`
	ConversationEnd   *events.ConversationEndEvent   `json:"conversation_end,omitempty"`
	ConversationError *events.ConversationErrorEvent `json:"conversation_error,omitempty"`
	ConversationTurn  *events.ConversationTurnEvent  `json:"conversation_turn,omitempty"`

	// LLM Events
	LLMGenerationStart     *events.LLMGenerationStartEvent     `json:"llm_generation_start,omitempty"`
	LLMGenerationEnd       *events.LLMGenerationEndEvent       `json:"llm_generation_end,omitempty"`
	LLMGenerationError     *events.LLMGenerationErrorEvent     `json:"llm_generation_error,omitempty"`
	LLMGenerationWithRetry *events.LLMGenerationWithRetryEvent `json:"llm_generation_with_retry,omitempty"`

	// Tool Events
	ToolCallStart *events.ToolCallStartEvent `json:"tool_call_start,omitempty"`
	ToolCallEnd   *events.ToolCallEndEvent   `json:"tool_call_end,omitempty"`
	ToolCallError *events.ToolCallErrorEvent `json:"tool_call_error,omitempty"`
	ToolExecution *events.ToolExecutionEvent `json:"tool_execution,omitempty"`

	// MCP Server Events
	MCPServerSelection *events.MCPServerSelectionEvent `json:"mcp_server_selection,omitempty"`

	// System Events
	SystemPrompt *events.SystemPromptEvent `json:"system_prompt,omitempty"`
	UserMessage  *events.UserMessageEvent  `json:"user_message,omitempty"`

	// Token & Usage Events
	TokenUsage       *events.TokenUsageEvent       `json:"token_usage,omitempty"`
	MaxTurnsReached  *events.MaxTurnsReachedEvent  `json:"max_turns_reached,omitempty"`
	ContextCancelled *events.ContextCancelledEvent `json:"context_cancelled,omitempty"`

	// Large Output Events
	LargeToolOutputDetected       *events.LargeToolOutputDetectedEvent       `json:"large_tool_output_detected,omitempty"`
	LargeToolOutputFileWritten    *events.LargeToolOutputFileWrittenEvent    `json:"large_tool_output_file_written,omitempty"`
	LargeToolOutputFileWriteError *events.LargeToolOutputFileWriteErrorEvent `json:"large_tool_output_file_write_error,omitempty"`

	// Fallback & Resilience Events
	RetryAttempt *events.RetryAttemptEvent `json:"retry_attempt,omitempty"`

	// Cache Events

	// Unified Completion Event
	UnifiedCompletion *events.UnifiedCompletionEvent `json:"unified_completion,omitempty"`

	// Orchestrator Events (only End is emitted)
	OrchestratorEnd        *orchestrator_events.OrchestratorEndEvent        `json:"orchestrator_end,omitempty"`
	OrchestratorAgentStart *orchestrator_events.OrchestratorAgentStartEvent `json:"orchestrator_agent_start,omitempty"`
	OrchestratorAgentEnd   *orchestrator_events.OrchestratorAgentEndEvent   `json:"orchestrator_agent_end,omitempty"`
	OrchestratorAgentError *orchestrator_events.OrchestratorAgentErrorEvent `json:"orchestrator_agent_error,omitempty"`

	// Background Agent Events
	BackgroundAgentStarted    *orchestrator_events.BackgroundAgentStartedEvent    `json:"background_agent_started,omitempty"`
	BackgroundAgentCompleted  *orchestrator_events.BackgroundAgentCompletedEvent  `json:"background_agent_completed,omitempty"`
	BackgroundAgentTerminated *orchestrator_events.BackgroundAgentTerminatedEvent `json:"background_agent_terminated,omitempty"`
	CodingAgentBackgroundTask *events.CodingAgentBackgroundTaskEvent              `json:"coding_agent_background_task,omitempty"`
	CodingAgentQuestion       *events.CodingAgentQuestionEvent                    `json:"coding_agent_question,omitempty"`
	SyntheticTurnReady        *orchestrator_events.SyntheticTurnReadyEvent        `json:"synthetic_turn_ready,omitempty"`
	AutoNotificationSteered   *orchestrator_events.AutoNotificationSteeredEvent   `json:"auto_notification_steered,omitempty"`

	// Presentation Events
	PresentationUpdated *orchestrator_events.PresentationUpdatedEvent `json:"presentation_updated,omitempty"`
	ProductInteraction  *orchestrator_events.ProductInteractionEvent  `json:"product_interaction,omitempty"`

	// Step Execution Events
	StepTokenUsage         *todo_creation_human.StepTokenUsageEvent         `json:"step_token_usage,omitempty"`
	RoutingEvaluated       *todo_creation_human.RoutingEvaluatedEvent       `json:"routing_evaluated,omitempty"`
	PreValidationCompleted *todo_creation_human.PreValidationCompletedEvent `json:"pre_validation_completed,omitempty"`
	ScriptedExecution      *orchestrator_events.ScriptedExecutionEvent      `json:"learn_code_script_execution,omitempty"`

	// Todo/Planning Events
	VariablesExtracted *todo_creation_human.VariablesExtractedEvent `json:"variables_extracted,omitempty"`

	// Human Feedback Events
	RequestHumanFeedback  *orchestrator_events.RequestHumanFeedbackEvent  `json:"request_human_feedback,omitempty"`
	BlockingHumanFeedback *orchestrator_events.BlockingHumanFeedbackEvent `json:"blocking_human_feedback,omitempty"`

	// Streaming Events
	StreamingStart *events.StreamingStartEvent `json:"streaming_start,omitempty"`
	StreamingChunk *events.StreamingChunkEvent `json:"streaming_chunk,omitempty"`
	StreamingEnd   *events.StreamingEndEvent   `json:"streaming_end,omitempty"`

	// MCP Server Connection Detail Events
	MCPServerConnectionStart *events.MCPServerConnectionStartEvent `json:"mcp_server_connection_start,omitempty"`
	MCPServerConnectionEnd   *events.MCPServerConnectionEndEvent   `json:"mcp_server_connection_end,omitempty"`

	// JSON Validation Events

	// Other Events
	ConversationThinking *events.ConversationThinkingEvent `json:"conversation_thinking,omitempty"`
}

// =============================================================================
// SECTION 3: Discriminated Union Schema Generation
// =============================================================================

// EventRegistry is the canonical list of event types exposed on the wire. Its
// value names the matching EventDataUnion JSON property. validateEventRegistry
// makes a missing or duplicate registration a generation failure.
var EventRegistry = map[events.EventType]string{
	// Core Agent Events
	events.AgentStart: "agent_start",
	events.AgentEnd:   "agent_end",
	events.AgentError: "agent_error",

	// Conversation Events
	events.ConversationStart: "conversation_start",
	events.ConversationEnd:   "conversation_end",
	events.ConversationError: "conversation_error",
	events.ConversationTurn:  "conversation_turn",

	// LLM Events
	events.LLMGenerationStart:     "llm_generation_start",
	events.LLMGenerationEnd:       "llm_generation_end",
	events.LLMGenerationError:     "llm_generation_error",
	events.LLMGenerationWithRetry: "llm_generation_with_retry",

	// Tool Events
	events.ToolCallStart: "tool_call_start",
	events.ToolCallEnd:   "tool_call_end",
	events.ToolCallError: "tool_call_error",
	events.ToolExecution: "tool_execution",

	// MCP Server Events
	events.MCPServerSelection: "mcp_server_selection",

	// System Events
	events.SystemPrompt: "system_prompt",
	events.UserMessage:  "user_message",

	// Token & Usage Events
	events.TokenUsage:       "token_usage",
	events.MaxTurnsReached:  "max_turns_reached",
	events.ContextCancelled: "context_cancelled",

	// Large Output Events
	events.LargeToolOutputDetected:       "large_tool_output_detected",
	events.LargeToolOutputFileWritten:    "large_tool_output_file_written",
	events.LargeToolOutputFileWriteError: "large_tool_output_file_write_error",

	// Fallback & Resilience Events
	events.RetryAttempt: "retry_attempt",

	// Cache Events (comprehensive only - specific cache events are below)

	// Unified Completion Event
	events.EventTypeUnifiedCompletion: "unified_completion",

	// Orchestrator Events (only End is emitted)
	orchestrator_events.OrchestratorEnd:        "orchestrator_end",
	orchestrator_events.OrchestratorAgentStart: "orchestrator_agent_start",
	orchestrator_events.OrchestratorAgentEnd:   "orchestrator_agent_end",
	orchestrator_events.OrchestratorAgentError: "orchestrator_agent_error",

	// Background Agent Events
	orchestrator_events.BackgroundAgentStarted:    "background_agent_started",
	orchestrator_events.BackgroundAgentCompleted:  "background_agent_completed",
	orchestrator_events.BackgroundAgentTerminated: "background_agent_terminated",
	events.CodingAgentBackgroundTask:              "coding_agent_background_task",
	events.CodingAgentQuestion:                    "coding_agent_question",
	orchestrator_events.SyntheticTurnReady:        "synthetic_turn_ready",
	orchestrator_events.AutoNotificationSteered:   "auto_notification_steered",

	// Presentation Events
	orchestrator_events.PresentationUpdated: "presentation_updated",
	orchestrator_events.ProductInteraction:  "product_interaction",

	// Step Execution Events
	orchestrator_events.StepTokenUsage:         "step_token_usage",
	orchestrator_events.RoutingEvaluated:       "routing_evaluated",
	orchestrator_events.PreValidationCompleted: "pre_validation_completed",
	orchestrator_events.ScriptedExecution:      "learn_code_script_execution",

	// Todo/Planning Events
	orchestrator_events.VariablesExtracted: "variables_extracted",

	// Human Feedback Events
	orchestrator_events.RequestHumanFeedback:  "request_human_feedback",
	orchestrator_events.BlockingHumanFeedback: "blocking_human_feedback",

	// Streaming Events
	events.StreamingStart: "streaming_start",
	events.StreamingChunk: "streaming_chunk",
	events.StreamingEnd:   "streaming_end",

	// MCP Server Connection Detail Events
	events.MCPServerConnectionStart: "mcp_server_connection_start",
	events.MCPServerConnectionEnd:   "mcp_server_connection_end",

	// JSON Validation Events

	// Other Events
	events.ConversationThinking: "conversation_thinking",
}

// =============================================================================
// SECTION 4: Schema Generation Functions
// =============================================================================

// schemaOnlyPayloadKeys are represented in the legacy schema but deliberately
// have no standalone wire event discriminator.
var schemaOnlyPayloadKeys = map[string]struct{}{}

func eventPayloadKeys() (map[string]struct{}, error) {
	keys := make(map[string]struct{})
	typeOfUnion := reflect.TypeOf(EventDataUnion{})
	for i := 0; i < typeOfUnion.NumField(); i++ {
		field := typeOfUnion.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			return nil, fmt.Errorf("EventDataUnion.%s has no JSON payload key", field.Name)
		}
		if _, exists := keys[name]; exists {
			return nil, fmt.Errorf("EventDataUnion contains duplicate payload key %q", name)
		}
		keys[name] = struct{}{}
	}
	return keys, nil
}

func validateEventRegistry() error {
	payloadKeys, err := eventPayloadKeys()
	if err != nil {
		return err
	}

	registeredPayloads := make(map[string]events.EventType, len(EventRegistry))
	for eventType, payloadKey := range EventRegistry {
		if eventType == "" {
			return fmt.Errorf("event registry contains an empty event type")
		}
		if _, exists := payloadKeys[payloadKey]; !exists {
			return fmt.Errorf("event registry %q points to missing EventDataUnion payload %q", eventType, payloadKey)
		}
		if previous, exists := registeredPayloads[payloadKey]; exists {
			return fmt.Errorf("EventDataUnion payload %q is registered twice: %q and %q", payloadKey, previous, eventType)
		}
		registeredPayloads[payloadKey] = eventType
	}
	for payloadKey := range schemaOnlyPayloadKeys {
		if _, exists := payloadKeys[payloadKey]; !exists {
			return fmt.Errorf("schema-only payload %q does not exist in EventDataUnion", payloadKey)
		}
		if eventType, registered := registeredPayloads[payloadKey]; registered {
			return fmt.Errorf("schema-only payload %q is also registered as %q", payloadKey, eventType)
		}
	}

	for payloadKey := range payloadKeys {
		if _, schemaOnly := schemaOnlyPayloadKeys[payloadKey]; schemaOnly {
			continue
		}
		if _, registered := registeredPayloads[payloadKey]; !registered {
			return fmt.Errorf("EventDataUnion payload %q is not registered; add it to EventRegistry or schemaOnlyPayloadKeys", payloadKey)
		}
	}
	return nil
}

func registeredEventTypes() []string {
	eventTypes := make([]string, 0, len(EventRegistry))
	for eventType := range EventRegistry {
		eventTypes = append(eventTypes, string(eventType))
	}
	sort.Strings(eventTypes)
	return eventTypes
}

func writeSchema(filename string, v any) error {
	r := new(jsonschema.Reflector)
	r.ExpandedStruct = true
	r.DoNotReference = false
	r.RequiredFromJSONSchemaTags = true

	schema := r.Reflect(v)

	// Ensure the output directory exists
	dir := filepath.Dir(filename)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Keep generated output deterministic even though jsonschema uses ordered
	// maps internally.
	schemaBytes, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("marshal schema: %w", err)
	}
	var schemaMap map[string]any
	if err := json.Unmarshal(schemaBytes, &schemaMap); err != nil {
		return fmt.Errorf("normalize schema: %w", err)
	}
	finalBytes, err := json.MarshalIndent(schemaMap, "", "  ")
	if err != nil {
		return fmt.Errorf("format schema: %w", err)
	}
	//nolint:gosec // G304: filename is fixed by the generator entrypoint.
	if err := os.WriteFile(filename, append(finalBytes, '\n'), 0644); err != nil {
		return fmt.Errorf("write schema %s: %w", filename, err)
	}
	return nil
}

// generateDiscriminatedUnionSchema generates a JSON schema with proper oneOf discriminated union
func generateDiscriminatedUnionSchema(filename string) error {
	r := new(jsonschema.Reflector)
	r.ExpandedStruct = true
	r.DoNotReference = false
	r.RequiredFromJSONSchemaTags = true

	// Generate the base schema first to get all definitions
	baseSchema := r.Reflect(&PollingEventActual{})

	// Generate all event type enum values. Go map iteration order is
	// non-deterministic, so sort the output to keep the generated schema
	// stable across runs (required by the drift-check pre-commit hook).
	eventTypeStrings := registeredEventTypes()
	eventTypes := make([]interface{}, 0, len(eventTypeStrings))
	for _, s := range eventTypeStrings {
		eventTypes = append(eventTypes, s)
	}

	// Add EventType enum to definitions if not present
	if baseSchema.Definitions == nil {
		baseSchema.Definitions = make(jsonschema.Definitions)
	}

	// Create EventType enum schema
	eventTypeSchema := &jsonschema.Schema{
		Type: "string",
		Enum: eventTypes,
	}
	baseSchema.Definitions["EventType"] = eventTypeSchema

	// Ensure the output directory exists
	dir := filepath.Dir(filename)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	//nolint:gosec // G304: filename comes from command-line/config, not user input
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", filename, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(baseSchema)
}

// =============================================================================
// SECTION 5: Legacy Types (for backward compatibility)
// =============================================================================

// UnifiedEvent represents a container for all event types (legacy, for unified-events-complete.schema.json)
type UnifiedEvent struct {
	// MCP Agent Events (from unified events package)
	ToolCallStartEvent      events.ToolCallStartEvent      `json:"tool_call_start"`
	ToolCallEndEvent        events.ToolCallEndEvent        `json:"tool_call_end"`
	ToolCallErrorEvent      events.ToolCallErrorEvent      `json:"tool_call_error"`
	LLMGenerationStartEvent events.LLMGenerationStartEvent `json:"llm_generation_start"`
	LLMGenerationEndEvent   events.LLMGenerationEndEvent   `json:"llm_generation_end"`
	MCPAgentStartEvent      events.AgentStartEvent         `json:"agent_start"`
	MCPAgentEndEvent        events.AgentEndEvent           `json:"agent_end"`
	MCPAgentErrorEvent      events.AgentErrorEvent         `json:"mcp_agent_error"`
	ConversationErrorEvent  events.ConversationErrorEvent  `json:"conversation_error"`
	LLMGenerationErrorEvent events.LLMGenerationErrorEvent `json:"llm_generation_error"`
	MCPServerSelectionEvent events.MCPServerSelectionEvent `json:"mcp_server_selection"`
	ConversationStartEvent  events.ConversationStartEvent  `json:"conversation_start"`
	ConversationEndEvent    events.ConversationEndEvent    `json:"conversation_end"`
	ConversationTurnEvent   events.ConversationTurnEvent   `json:"conversation_turn"`

	SystemPromptEvent events.SystemPromptEvent `json:"system_prompt"`
	UserMessageEvent  events.UserMessageEvent  `json:"user_message"`

	LargeToolOutputDetectedEvent    events.LargeToolOutputDetectedEvent    `json:"large_tool_output_detected"`
	LargeToolOutputFileWrittenEvent events.LargeToolOutputFileWrittenEvent `json:"large_tool_output_file_written"`
	TokenUsageEvent                 events.TokenUsageEvent                 `json:"token_usage"`
	MaxTurnsReachedEvent            events.MaxTurnsReachedEvent            `json:"max_turns_reached"`
	ContextCancelledEvent           events.ContextCancelledEvent           `json:"context_cancelled"`

	// Additional MCP Agent Events that exist in backend

	RetryAttemptEvent           events.RetryAttemptEvent           `json:"retry_attempt"`
	ToolExecutionEvent          events.ToolExecutionEvent          `json:"tool_execution"`
	LLMGenerationWithRetryEvent events.LLMGenerationWithRetryEvent `json:"llm_generation_with_retry"`

	// Orchestrator Events - now handled by unified events system (only End is emitted)
	OrchestratorEndEvent        orchestrator_events.OrchestratorEndEvent        `json:"orchestrator_end"`
	OrchestratorAgentStartEvent orchestrator_events.OrchestratorAgentStartEvent `json:"orchestrator_agent_start"`
	OrchestratorAgentEndEvent   orchestrator_events.OrchestratorAgentEndEvent   `json:"orchestrator_agent_end"`
	OrchestratorAgentErrorEvent orchestrator_events.OrchestratorAgentErrorEvent `json:"orchestrator_agent_error"`

	// Background Agent Events
	BackgroundAgentStartedEvent    orchestrator_events.BackgroundAgentStartedEvent    `json:"background_agent_started"`
	BackgroundAgentCompletedEvent  orchestrator_events.BackgroundAgentCompletedEvent  `json:"background_agent_completed"`
	BackgroundAgentTerminatedEvent orchestrator_events.BackgroundAgentTerminatedEvent `json:"background_agent_terminated"`
	CodingAgentBackgroundTaskEvent events.CodingAgentBackgroundTaskEvent              `json:"coding_agent_background_task"`
	CodingAgentQuestionEvent       events.CodingAgentQuestionEvent                    `json:"coding_agent_question"`
	SyntheticTurnReadyEvent        orchestrator_events.SyntheticTurnReadyEvent        `json:"synthetic_turn_ready"`
	AutoNotificationSteeredEvent   orchestrator_events.AutoNotificationSteeredEvent   `json:"auto_notification_steered"`
	PresentationUpdatedEvent       orchestrator_events.PresentationUpdatedEvent       `json:"presentation_updated"`
	ProductInteractionEvent        orchestrator_events.ProductInteractionEvent        `json:"product_interaction"`

	// Human Verification Events
	RequestHumanFeedbackEvent orchestrator_events.RequestHumanFeedbackEvent `json:"request_human_feedback"`

	// Step Execution Events
	StepTokenUsageEvent         todo_creation_human.StepTokenUsageEvent         `json:"step_token_usage"`
	PreValidationCompletedEvent todo_creation_human.PreValidationCompletedEvent `json:"pre_validation_completed"`
	ScriptedExecutionEvent      orchestrator_events.ScriptedExecutionEvent      `json:"learn_code_script_execution"`

	// Todo/Planning Events
	VariablesExtractedEvent todo_creation_human.VariablesExtractedEvent `json:"variables_extracted"`

	// Large Output Error Events
	LargeToolOutputFileWriteErrorEvent events.LargeToolOutputFileWriteErrorEvent `json:"large_tool_output_file_write_error"`

	// Nested types that need to be included in schema (not events themselves)
	// TodoStep is used by frontend but not directly in events, so we include it here to ensure it's generated
	TodoStep orchestrator_events.TodoStep `json:"todo_step,omitempty"`
}

// =============================================================================
// SECTION 6: Main Entry Point
// =============================================================================

func main() {
	fmt.Println("Generating JSON schemas for event types...")
	if err := validateEventRegistry(); err != nil {
		fmt.Printf("Invalid event schema registry: %v\n", err)
		os.Exit(1)
	}

	// Generate unified events schema. When run from agent_go/, schemas/ is
	// the frontend-consumed path.
	if err := writeSchema("schemas/unified-events-complete.schema.json", UnifiedEvent{}); err != nil {
		fmt.Printf("Error generating unified events schema: %v\n", err)
		os.Exit(1)
	}
	// Generate the new PollingEvent schema with proper wire format.
	if err := generateDiscriminatedUnionSchema("schemas/polling-event.schema.json"); err != nil {
		fmt.Printf("Error generating polling event schema: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ Successfully generated schemas:")
	fmt.Println("  - schemas/unified-events-complete.schema.json")
	fmt.Println("  - schemas/polling-event.schema.json")
	fmt.Println("")
	fmt.Println("📋 Schema Structure (matching actual wire format):")
	fmt.Println("  PollingEventActual")
	fmt.Println("  ├── id: string")
	fmt.Println("  ├── type: EventType (discriminator)")
	fmt.Println("  ├── timestamp: string")
	fmt.Println("  ├── session_id?: string")
	fmt.Println("  ├── error?: string")
	fmt.Println("  └── data: AgentEventForSchema")
	fmt.Println("       ├── type: EventType")
	fmt.Println("       ├── timestamp: string")
	fmt.Println("       ├── event_index: number")
	fmt.Println("       ├── trace_id?: string")
	fmt.Println("       ├── ... (hierarchy fields)")
	fmt.Println("       └── data: EventDataUnion (typed event data)")
}
