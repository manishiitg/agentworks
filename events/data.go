package events

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// AgentEventType represents the type of event in the agent flow
// Note: EventType constants are now defined in types.go

type AgentEventType = EventType

// AgentEvent represents a generic agent event with typed data
type AgentEvent struct {
	Type          EventType `json:"type"`
	Timestamp     time.Time `json:"timestamp"`
	EventIndex    int       `json:"event_index"`
	TraceID       string    `json:"trace_id,omitempty"`
	SpanID        string    `json:"span_id,omitempty"`
	ParentID      string    `json:"parent_id,omitempty"`
	CorrelationID string    `json:"correlation_id,omitempty"` // Links start/end event pairs
	Data          EventData `json:"data"`

	// NEW: Hierarchy fields for frontend tree structure
	HierarchyLevel int    `json:"hierarchy_level"`      // 0=root, 1=child, 2=grandchild
	SessionID      string `json:"session_id,omitempty"` // Group related events
	Component      string `json:"component,omitempty"`  // orchestrator, agent, llm, tool
	TurnID         string `json:"turn_id,omitempty"`    // Stable identity of one accepted message/turn
}

// Getter methods to implement observability.AgentEvent interface
func (e *AgentEvent) GetType() string {
	return string(e.Type)
}

func (e *AgentEvent) GetCorrelationID() string {
	return e.CorrelationID
}

func (e *AgentEvent) GetTimestamp() time.Time {
	return e.Timestamp
}

func (e *AgentEvent) GetData() interface{} {
	return e.Data
}

func (e *AgentEvent) GetTraceID() string {
	return e.TraceID
}

func (e *AgentEvent) GetParentID() string {
	return e.ParentID
}

// GenericEventData carries unstructured payloads for application-defined outer event types.
type GenericEventData struct {
	BaseEventData
	Data map[string]interface{} `json:"data"`
}

func (e *GenericEventData) GetEventType() EventType {
	return EventType("generic")
}

const (
	BrokenPipeOpDetected     = "broken_pipe_detected"
	BrokenPipeOpRetrySuccess = "retry_success"
	BrokenPipeOpRetryFailure = "retry_failure"
)

// BrokenPipeEvent represents MCP connection broken pipe detection and retry events
type BrokenPipeEvent struct {
	BaseEventData
	Operation  string `json:"operation"` // BrokenPipeOp* constants
	ToolName   string `json:"tool_name"`
	ServerName string `json:"server_name"`
	ToolCallID string `json:"tool_call_id"`
	Error      string `json:"error,omitempty"`
	Duration   string `json:"duration,omitempty"`
}

func (e *BrokenPipeEvent) GetEventType() EventType {
	return BrokenPipe
}

// AgentStartEvent represents the start of an agent session
type AgentStartEvent struct {
	BaseEventData
	AgentType            string `json:"agent_type"`
	ModelID              string `json:"model_id"`
	Provider             string `json:"provider"`
	UseCodeExecutionMode bool   `json:"use_code_execution_mode,omitempty"`
}

func (e *AgentStartEvent) GetEventType() EventType {
	return AgentStart
}

// AgentEndEvent represents the end of an agent session
type AgentEndEvent struct {
	BaseEventData
	AgentType             string `json:"agent_type"`
	Success               bool   `json:"success"`
	Error                 string `json:"error,omitempty"`
	PromptTokens          int    `json:"prompt_tokens,omitempty"`
	CompletionTokens      int    `json:"completion_tokens,omitempty"`
	TotalTokens           int    `json:"total_tokens,omitempty"`
	CacheTokens           int    `json:"cache_tokens,omitempty"`
	ReasoningTokens       int    `json:"reasoning_tokens,omitempty"`
	LLMCallCount          int    `json:"llm_call_count,omitempty"`
	CacheEnabledCallCount int    `json:"cache_enabled_call_count,omitempty"`
}

func (e *AgentEndEvent) GetEventType() EventType {
	return AgentEnd
}

// AgentErrorEvent represents an agent error
type AgentErrorEvent struct {
	BaseEventData
	Error    string        `json:"error"`
	Turn     int           `json:"turn"`
	Context  string        `json:"context"`
	Duration time.Duration `json:"duration"`
}

func (e *AgentErrorEvent) GetEventType() EventType {
	return AgentError
}

// ConversationStartEvent represents the start of a conversation
type ConversationStartEvent struct {
	BaseEventData
	Question     string `json:"question"`
	SystemPrompt string `json:"system_prompt"`
	ToolsCount   int    `json:"tools_count"`
	Servers      string `json:"servers"`
}

func (e *ConversationStartEvent) GetEventType() EventType {
	return ConversationStart
}

// SerializedMessage represents a message that can be properly serialized to JSON
type SerializedMessage struct {
	Role  string        `json:"role"`
	Parts []MessagePart `json:"parts,omitempty"`
}

// ToolInfo represents information about a tool available to the LLM
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Server      string `json:"server"`
}

// ConvertToolsToToolInfo converts llmtypes.Tool to ToolInfo slice
func ConvertToolsToToolInfo(tools []llmtypes.Tool, toolToServer map[string]string) []ToolInfo {
	var toolInfos []ToolInfo
	for _, tool := range tools {
		serverName := "unknown"
		if mappedServer, exists := toolToServer[tool.Function.Name]; exists {
			serverName = mappedServer
		}
		toolInfos = append(toolInfos, ToolInfo{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Server:      serverName,
		})
	}
	return toolInfos
}

// MessagePart represents a serializable message part
type MessagePart struct {
	Type    string      `json:"type"`
	Content interface{} `json:"content"`
}

// ConversationTurnEvent represents a conversation turn
type ConversationTurnEvent struct {
	BaseEventData
	Turn           int                 `json:"turn"`
	Question       string              `json:"question"`
	MessagesCount  int                 `json:"messages_count"`
	HasToolCalls   bool                `json:"has_tool_calls"`
	ToolCallsCount int                 `json:"tool_calls_count"`
	Tools          []ToolInfo          `json:"tools,omitempty"`
	Messages       []SerializedMessage `json:"messages,omitempty"`
}

func (e *ConversationTurnEvent) GetEventType() EventType {
	return ConversationTurn
}

// serializeMessage converts llmtypes.MessageContent to SerializedMessage
func serializeMessage(msg llmtypes.MessageContent) SerializedMessage {
	serialized := SerializedMessage{
		Role:  string(msg.Role),
		Parts: []MessagePart{},
	}

	if msg.Parts != nil {
		for _, part := range msg.Parts {
			messagePart := MessagePart{}

			switch p := part.(type) {
			case llmtypes.TextContent:
				messagePart.Type = "text"
				messagePart.Content = p.Text
			case llmtypes.ImageContent:
				messagePart.Type = "image"
				// Store metadata only, not full base64 data (too large for events)
				imageMeta := map[string]interface{}{
					"source_type": p.SourceType,
					"media_type":  p.MediaType,
				}
				if p.SourceType == "url" {
					// Include URL since it's not as large as base64 data
					imageMeta["url"] = p.Data
				} else {
					// For base64, just indicate data length
					imageMeta["data_length"] = len(p.Data)
					imageMeta["data_preview"] = "base64_encoded_image_data"
				}
				messagePart.Content = imageMeta
			case llmtypes.ToolCall:
				messagePart.Type = "tool_call"
				messagePart.Content = map[string]interface{}{
					"id":            p.ID,
					"function_name": p.FunctionCall.Name,
					"function_args": p.FunctionCall.Arguments,
				}
			case llmtypes.ToolCallResponse:
				messagePart.Type = "tool_response"
				messagePart.Content = map[string]interface{}{
					"tool_call_id": p.ToolCallID,
					"content":      p.Content,
				}
			default:
				messagePart.Type = "unknown"
				messagePart.Content = fmt.Sprintf("%T: %+v", part, part)
			}

			serialized.Parts = append(serialized.Parts, messagePart)
		}
	}

	return serialized
}

// LLMGenerationStartEvent represents the start of LLM generation
type LLMGenerationStartEvent struct {
	BaseEventData
	Turn          int     `json:"turn"`
	ModelID       string  `json:"model_id"`
	Temperature   float64 `json:"temperature"`
	ToolsCount    int     `json:"tools_count"`
	MessagesCount int     `json:"messages_count"`
}

func (e *LLMGenerationStartEvent) GetEventType() EventType {
	return LLMGenerationStart
}

// LLMGenerationEndEvent represents the completion of LLM generation
type LLMGenerationEndEvent struct {
	BaseEventData
	Turn         int           `json:"turn"`
	Content      string        `json:"content"`
	ToolCalls    int           `json:"tool_calls"`
	Duration     time.Duration `json:"duration"`
	UsageMetrics UsageMetrics  `json:"usage_metrics"`
}

func (e *LLMGenerationEndEvent) GetEventType() EventType {
	return LLMGenerationEnd
}

// UsageMetrics represents LLM usage metrics
type UsageMetrics struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CacheTokens      int `json:"cache_tokens,omitempty"`     // Cache tokens (CachedContentTokens, CacheReadInputTokens, etc.)
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"` // Reasoning tokens (for models like o3)
}

// ToolCallStartEvent represents the start of a tool call
type ToolCallStartEvent struct {
	BaseEventData
	Turn       int        `json:"turn"`
	ToolName   string     `json:"tool_name"`
	ToolParams ToolParams `json:"tool_params"`
	ServerName string     `json:"server_name"`
	IsParallel bool       `json:"is_parallel"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // Unique ID from the LLM response, used to correlate start/end/error events
}

func (e *ToolCallStartEvent) GetEventType() EventType {
	return ToolCallStart
}

// ToolParams represents tool call parameters
type ToolParams struct {
	Arguments string `json:"arguments"`
}

// ToolCallRecord is the portable, completed-or-in-flight view of a tool call.
// It is deliberately derived from the typed start/end/error events rather than
// from any coding CLI's transcript format, so every consumer can render one
// contract for arguments, result/error, duration and execution state.
type ToolCallRecord struct {
	ToolCallID string        `json:"tool_call_id,omitempty"`
	ToolName   string        `json:"tool_name"`
	ServerName string        `json:"server_name,omitempty"`
	Arguments  string        `json:"arguments,omitempty"`
	Result     string        `json:"result,omitempty"`
	Error      string        `json:"error,omitempty"`
	Duration   time.Duration `json:"duration,omitempty"`
	Status     string        `json:"status"`
}

// ToolCallRecordFromEvent applies one canonical lifecycle event to a tool-call
// record. Callers keep records keyed by ToolCallID; a start creates a running
// record and end/error completes it.
func ToolCallRecordFromEvent(event EventData) (ToolCallRecord, bool) {
	switch e := event.(type) {
	case *ToolCallStartEvent:
		return ToolCallRecord{ToolCallID: e.ToolCallID, ToolName: e.ToolName, ServerName: e.ServerName, Arguments: e.ToolParams.Arguments, Status: "running"}, true
	case *ToolCallEndEvent:
		return ToolCallRecord{ToolCallID: e.ToolCallID, ToolName: e.ToolName, ServerName: e.ServerName, Result: e.Result, Duration: e.Duration, Status: "completed"}, true
	case *ToolCallErrorEvent:
		return ToolCallRecord{ToolCallID: e.ToolCallID, ToolName: e.ToolName, ServerName: e.ServerName, Error: e.Error, Duration: e.Duration, Status: "failed"}, true
	default:
		return ToolCallRecord{}, false
	}
}

// ToolCallEndEvent represents the completion of a tool call
type ToolCallEndEvent struct {
	BaseEventData
	Turn       int           `json:"turn"`
	ToolName   string        `json:"tool_name"`
	Result     string        `json:"result"`
	Duration   time.Duration `json:"duration"`
	ServerName string        `json:"server_name"`
	ToolCallID string        `json:"tool_call_id,omitempty"` // Unique ID from the LLM response, used to correlate start/end/error events
	// SyntheticSettle marks an end event the PLATFORM fabricated because the
	// tool call never reported one of its own (agent_go's settleOpenToolCalls,
	// PLAT-141), rather than one the provider actually sent.
	//
	// It exists because Duration means something DIFFERENT on these events: it
	// is how long the call sat open before being force-closed at turn end, not
	// how long the tool ran. The backend already logs that distinction
	// verbatim ("open-to-settle (NOT tool runtime)"), but nothing carried it to
	// a consumer, so the UI rendered open-to-settle time under a "Duration"
	// label and a reader reasonably concluded the tools were slow. Observed
	// 2026-08-19: a Codex chat session settling 14 of 14 unreported calls, all
	// stamped the same second, showing durations up to 6.3m for tools that had
	// not necessarily taken any such time.
	//
	// Absent/false means the provider genuinely reported the end and Duration
	// is real runtime.
	SyntheticSettle bool `json:"synthetic_settle,omitempty"`
	// Token usage information (optional)
	ContextUsagePercent float64 `json:"context_usage_percent,omitempty"`
	ModelContextWindow  int     `json:"model_context_window,omitempty"`
	ContextWindowUsage  int     `json:"context_window_usage,omitempty"`
	// Model information (optional) - shows which model is being used
	ModelID string `json:"model_id,omitempty"`
}

func (e *ToolCallEndEvent) GetEventType() EventType {
	return ToolCallEnd
}

// MCPServerConnectionEvent represents MCP server connection
type MCPServerConnectionEvent struct {
	BaseEventData
	ServerName     string                 `json:"server_name"`
	ConfigPath     string                 `json:"config_path,omitempty"`
	Timeout        string                 `json:"timeout,omitempty"`
	Operation      string                 `json:"operation,omitempty"`
	Status         string                 `json:"status"`
	ToolsCount     int                    `json:"tools_count"`
	ConnectionTime time.Duration          `json:"connection_time"`
	Error          string                 `json:"error,omitempty"`
	ServerInfo     map[string]interface{} `json:"server_info,omitempty"`
}

func (e *MCPServerConnectionEvent) GetEventType() EventType {
	return MCPServerConnectionStart
}

// MCPServerSelectionEvent represents MCP server selection for a query
type MCPServerSelectionEvent struct {
	BaseEventData
	Turn            int      `json:"turn"`
	SelectedServers []string `json:"selected_servers"`
	TotalServers    int      `json:"total_servers"`
	Source          string   `json:"source"` // "preset", "manual", "all"
	Query           string   `json:"query"`
}

func (e *MCPServerSelectionEvent) GetEventType() EventType {
	return MCPServerSelection
}

// ConversationEndEvent represents the end of a conversation
type ConversationEndEvent struct {
	BaseEventData
	Question string        `json:"question"`
	Result   string        `json:"result"`
	Duration time.Duration `json:"duration"`
	Turns    int           `json:"turns"`
	Status   string        `json:"status"`
	Error    string        `json:"error,omitempty"`
}

func (e *ConversationEndEvent) GetEventType() EventType {
	return ConversationEnd
}

// ConversationErrorEvent represents a conversation error
type ConversationErrorEvent struct {
	BaseEventData
	Question string        `json:"question"`
	Error    string        `json:"error"`
	Turn     int           `json:"turn"`
	Context  string        `json:"context"`
	Duration time.Duration `json:"duration"`
}

func (e *ConversationErrorEvent) GetEventType() EventType {
	return ConversationError
}

// LLMGenerationErrorEvent represents an LLM generation error
type LLMGenerationErrorEvent struct {
	BaseEventData
	Turn     int           `json:"turn"`
	ModelID  string        `json:"model_id"`
	Error    string        `json:"error"`
	Duration time.Duration `json:"duration"`
}

func (e *LLMGenerationErrorEvent) GetEventType() EventType {
	return LLMGenerationError
}

// ToolCallErrorEvent represents a tool call error
type ToolCallErrorEvent struct {
	BaseEventData
	Turn       int           `json:"turn"`
	ToolName   string        `json:"tool_name"`
	Error      string        `json:"error"`
	ServerName string        `json:"server_name"`
	Duration   time.Duration `json:"duration"`
	ToolCallID string        `json:"tool_call_id,omitempty"` // Unique ID from the LLM response, used to correlate start/end/error events
}

func (e *ToolCallErrorEvent) GetEventType() EventType {
	return ToolCallError
}

// TokenUsageEvent represents detailed token usage information
type TokenUsageEvent struct {
	BaseEventData
	Turn             int           `json:"turn"`
	Operation        string        `json:"operation"`
	PromptTokens     int           `json:"prompt_tokens"`
	CompletionTokens int           `json:"completion_tokens"`
	TotalTokens      int           `json:"total_tokens"`
	ModelID          string        `json:"model_id"`
	Provider         string        `json:"provider"`
	CostEstimate     float64       `json:"cost_estimate,omitempty"`
	Duration         time.Duration `json:"duration"`
	Context          string        `json:"context"`
	// Agent mode information
	AgentMode            string `json:"agent_mode,omitempty"`
	UseCodeExecutionMode bool   `json:"use_code_execution_mode,omitempty"`
	// OpenRouter cache information
	CacheDiscount   float64 `json:"cache_discount,omitempty"`
	ReasoningTokens int     `json:"reasoning_tokens,omitempty"`
	// Pricing fields (in USD)
	InputCost     float64 `json:"input_cost_usd,omitempty"`
	OutputCost    float64 `json:"output_cost_usd,omitempty"`
	ReasoningCost float64 `json:"reasoning_cost_usd,omitempty"`
	CacheCost     float64 `json:"cache_cost_usd,omitempty"`
	TotalCost     float64 `json:"total_cost_usd,omitempty"`
	// Context window tracking
	ContextWindowUsage  int     `json:"context_window_usage,omitempty"`
	ModelContextWindow  int     `json:"model_context_window,omitempty"`
	ContextUsagePercent float64 `json:"context_usage_percent,omitempty"`
	// Raw GenerationInfo for debugging
	GenerationInfo map[string]interface{} `json:"generation_info,omitempty"`
}

func (e *TokenUsageEvent) GetEventType() EventType {
	return TokenUsage
}

// ToolContext represents tool information for LLM context
type ToolContext struct {
	ToolName   string `json:"tool_name"`
	ServerName string `json:"server_name"`
	Arguments  string `json:"arguments,omitempty"`
	Result     string `json:"result,omitempty"`
	Status     string `json:"status"`
}

// SystemPromptEvent represents a system prompt being used
type SystemPromptEvent struct {
	BaseEventData
	Content    string `json:"content"`
	Turn       int    `json:"turn"`
	TokenCount int    `json:"token_count,omitempty"`
}

func (e *SystemPromptEvent) GetEventType() EventType {
	return SystemPrompt
}

// UserMessageEvent represents a user message
type UserMessageEvent struct {
	BaseEventData
	Turn    int    `json:"turn"`
	Content string `json:"content"`
	Role    string `json:"role"`
}

func (e *UserMessageEvent) GetEventType() EventType {
	return UserMessage
}

// LiveInputConfirmedEvent upgrades a live-input user message row from
// fast pane confirmation to durable CLI-record proof. Outcome is one
// of "confirmed", "accepted_but_unflushed", or "failed".
type LiveInputConfirmedEvent struct {
	BaseEventData
	MessageID   string `json:"message_id"`
	Outcome     string `json:"outcome"`
	ProofSource string `json:"proof_source,omitempty"`
	LatencyMs   int64  `json:"latency_ms,omitempty"`
	Provider    string `json:"provider,omitempty"`
}

func (e *LiveInputConfirmedEvent) GetEventType() EventType {
	return LiveInputConfirmed
}

// NewLiveInputConfirmedEvent creates a new LiveInputConfirmedEvent.
// Metadata mirrors the user_message row so either envelope shape
// resolves the confirmed message by metadata.message_id.
func NewLiveInputConfirmedEvent(messageID, outcome, proofSource, provider string, latencyMs int64) *LiveInputConfirmedEvent {
	return &LiveInputConfirmedEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
			Metadata: map[string]interface{}{
				"source":       "coding_agent_live_input",
				"message_id":   messageID,
				"confirmation": outcome,
				"proof_source": proofSource,
				"latency_ms":   latencyMs,
				"provider":     provider,
			},
		},
		MessageID:   messageID,
		Outcome:     outcome,
		ProofSource: proofSource,
		LatencyMs:   latencyMs,
		Provider:    provider,
	}
}

// NewUserMessageEvent creates a new UserMessageEvent
func NewUserMessageEvent(turn int, content, role string) *UserMessageEvent {
	return &UserMessageEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		Turn:    turn,
		Content: content,
		Role:    role,
	}
}

// generateEventID generates a unique event ID
// GenerateEventID creates a unique event ID
func GenerateEventID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to time-based ID if random read fails
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}

// NewAgentEvent creates a new AgentEvent with typed data
func NewAgentEvent(eventData EventData) *AgentEvent {
	event := &AgentEvent{
		Type:           eventData.GetEventType(),
		Timestamp:      time.Now(),
		Data:           eventData,
		HierarchyLevel: 0, // Default to root level
	}
	if base, ok := eventData.(interface{ GetBaseEventData() *BaseEventData }); ok {
		metadata := base.GetBaseEventData().Metadata
		if metadata != nil {
			if turnID, ok := metadata["turn_id"].(string); ok {
				event.TurnID = turnID
			}
		}
	}
	return event
}

// NewAgentStartEvent creates a new AgentStartEvent
func NewAgentStartEvent(agentType, modelID, provider string, useCodeExecutionMode bool) *AgentStartEvent {
	return &AgentStartEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		AgentType:            agentType,
		ModelID:              modelID,
		Provider:             provider,
		UseCodeExecutionMode: useCodeExecutionMode,
	}
}

// NewAgentEndEventWithTokens creates a new AgentEndEvent with token usage information
func NewAgentEndEventWithTokens(agentType string, success bool, error string, promptTokens, completionTokens, totalTokens, cacheTokens, reasoningTokens, llmCallCount, cacheEnabledCallCount int) *AgentEndEvent {
	return &AgentEndEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		AgentType:             agentType,
		Success:               success,
		Error:                 error,
		PromptTokens:          promptTokens,
		CompletionTokens:      completionTokens,
		TotalTokens:           totalTokens,
		CacheTokens:           cacheTokens,
		ReasoningTokens:       reasoningTokens,
		LLMCallCount:          llmCallCount,
		CacheEnabledCallCount: cacheEnabledCallCount,
	}
}

// NewAgentErrorEvent creates a new AgentErrorEvent
func NewAgentErrorEvent(error string, turn int, context string, duration time.Duration) *AgentErrorEvent {
	return &AgentErrorEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		Error:    error,
		Turn:     turn,
		Context:  context,
		Duration: duration,
	}
}

// NewConversationStartEvent creates a new ConversationStartEvent
func NewConversationStartEvent(question, systemPrompt string, toolsCount int, servers string) *ConversationStartEvent {
	return &ConversationStartEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
			EventID:   GenerateEventID(),
		},
		Question:     question,
		SystemPrompt: systemPrompt,
		ToolsCount:   toolsCount,
		Servers:      servers,
	}
}

// NewConversationStartEventWithCorrelation creates a new ConversationStartEvent with correlation data
func NewConversationStartEventWithCorrelation(question, systemPrompt string, toolsCount int, servers string, traceID, parentID string) *ConversationStartEvent {
	return &ConversationStartEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
			TraceID:   traceID,
			EventID:   GenerateEventID(),
			ParentID:  parentID,
		},
		Question:     question,
		SystemPrompt: systemPrompt,
		ToolsCount:   toolsCount,
		Servers:      servers,
	}
}

// NewConversationEndEvent creates a new ConversationEndEvent
func NewConversationEndEvent(question, result string, duration time.Duration, turns int, status, error string) *ConversationEndEvent {
	return &ConversationEndEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		Question: question,
		Result:   result,
		Duration: duration,
		Turns:    turns,
		Status:   status,
		Error:    error,
	}
}

// NewConversationErrorEvent creates a new ConversationErrorEvent
func NewConversationErrorEvent(question, error string, turn int, context string, duration time.Duration) *ConversationErrorEvent {
	return &ConversationErrorEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		Question: question,
		Error:    error,
		Turn:     turn,
		Context:  context,
		Duration: duration,
	}
}

// NewConversationTurnEvent creates a new ConversationTurnEvent
func NewConversationTurnEvent(turn int, question string, messagesCount int, hasToolCalls bool, toolCallsCount int, tools []ToolInfo, messages []llmtypes.MessageContent) *ConversationTurnEvent {
	// Convert llmtypes.MessageContent to SerializedMessage, filtering out system messages
	var serializedMessages []SerializedMessage
	for _, msg := range messages {
		// Skip system messages
		if msg.Role == llmtypes.ChatMessageTypeSystem {
			continue
		}
		serializedMessages = append(serializedMessages, serializeMessage(msg))
	}

	return &ConversationTurnEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		Turn:           turn,
		Question:       question,
		MessagesCount:  messagesCount,
		HasToolCalls:   hasToolCalls,
		ToolCallsCount: toolCallsCount,
		Tools:          tools,
		Messages:       serializedMessages,
	}
}

// NewLLMGenerationStartEvent creates a new LLMGenerationStartEvent
func NewLLMGenerationStartEvent(turn int, modelID string, temperature float64, toolsCount, messagesCount int) *LLMGenerationStartEvent {
	return &LLMGenerationStartEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
			EventID:   GenerateEventID(),
		},
		Turn:          turn,
		ModelID:       modelID,
		Temperature:   temperature,
		ToolsCount:    toolsCount,
		MessagesCount: messagesCount,
	}
}

// NewLLMGenerationEndEvent creates a new LLMGenerationEndEvent
func NewLLMGenerationEndEvent(turn int, content string, toolCalls int, duration time.Duration, usageMetrics UsageMetrics) *LLMGenerationEndEvent {
	return &LLMGenerationEndEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		Turn:         turn,
		Content:      content,
		ToolCalls:    toolCalls,
		Duration:     duration,
		UsageMetrics: usageMetrics,
	}
}

// NewLLMGenerationErrorEvent creates a new LLMGenerationErrorEvent
func NewLLMGenerationErrorEvent(turn int, modelID string, error string, duration time.Duration) *LLMGenerationErrorEvent {
	return &LLMGenerationErrorEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
			EventID:   GenerateEventID(),
		},
		Turn:     turn,
		ModelID:  modelID,
		Error:    error,
		Duration: duration,
	}
}

// NewToolCallStartEvent creates a new ToolCallStartEvent
func NewToolCallStartEvent(turn int, toolName string, toolParams ToolParams, serverName string, spanID string) *ToolCallStartEvent {
	return &ToolCallStartEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
			EventID:   GenerateEventID(),
			SpanID:    spanID,
		},
		Turn:       turn,
		ToolName:   toolName,
		ToolParams: toolParams,
		ServerName: serverName,
	}
}

// NewToolCallStartEventWithCorrelation creates a new ToolCallStartEvent with correlation data
func NewToolCallStartEventWithCorrelation(turn int, toolName string, toolParams ToolParams, serverName string, traceID, parentID string) *ToolCallStartEvent {
	return &ToolCallStartEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
			TraceID:   traceID,
			EventID:   GenerateEventID(),
			ParentID:  parentID,
		},
		Turn:       turn,
		ToolName:   toolName,
		ToolParams: toolParams,
		ServerName: serverName,
	}
}

// NewToolCallEndEvent creates a new ToolCallEndEvent
func NewToolCallEndEvent(turn int, toolName, result, serverName string, duration time.Duration, spanID string) *ToolCallEndEvent {
	return &ToolCallEndEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
			SpanID:    spanID,
		},
		Turn:       turn,
		ToolName:   toolName,
		Result:     result,
		Duration:   duration,
		ServerName: serverName,
	}
}

// NewToolCallEndEventWithTokenUsage creates a new ToolCallEndEvent with token usage information
func NewToolCallEndEventWithTokenUsage(turn int, toolName, result, serverName string, duration time.Duration, spanID string, contextUsagePercent float64, modelContextWindow, contextWindowUsage int) *ToolCallEndEvent {
	return &ToolCallEndEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
			SpanID:    spanID,
		},
		Turn:                turn,
		ToolName:            toolName,
		Result:              result,
		Duration:            duration,
		ServerName:          serverName,
		ContextUsagePercent: contextUsagePercent,
		ModelContextWindow:  modelContextWindow,
		ContextWindowUsage:  contextWindowUsage,
	}
}

// NewToolCallEndEventWithTokenUsageAndModel creates a new ToolCallEndEvent with token usage and model information
func NewToolCallEndEventWithTokenUsageAndModel(turn int, toolName, result, serverName string, duration time.Duration, spanID string, contextUsagePercent float64, modelContextWindow, contextWindowUsage int, modelID string) *ToolCallEndEvent {
	return &ToolCallEndEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
			SpanID:    spanID,
		},
		Turn:                turn,
		ToolName:            toolName,
		Result:              result,
		Duration:            duration,
		ServerName:          serverName,
		ContextUsagePercent: contextUsagePercent,
		ModelContextWindow:  modelContextWindow,
		ContextWindowUsage:  contextWindowUsage,
		ModelID:             modelID,
	}
}

// NewToolCallErrorEvent creates a new ToolCallErrorEvent
func NewToolCallErrorEvent(turn int, toolName, error string, serverName string, duration time.Duration) *ToolCallErrorEvent {
	return &ToolCallErrorEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		Turn:       turn,
		ToolName:   toolName,
		Error:      error,
		ServerName: serverName,
		Duration:   duration,
	}
}

// NewMCPServerConnectionEvent creates a new MCPServerConnectionEvent
func NewMCPServerConnectionEvent(serverName, status string, toolsCount int, connectionTime time.Duration, error string) *MCPServerConnectionEvent {
	return &MCPServerConnectionEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		ServerName:     serverName,
		Status:         status,
		ToolsCount:     toolsCount,
		ConnectionTime: connectionTime,
		Error:          error,
	}
}

// NewMCPServerSelectionEvent creates a new MCPServerSelectionEvent
func NewMCPServerSelectionEvent(turn int, selectedServers []string, totalServers int, source, query string) *MCPServerSelectionEvent {
	return &MCPServerSelectionEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		Turn:            turn,
		SelectedServers: selectedServers,
		TotalServers:    totalServers,
		Source:          source,
		Query:           query,
	}
}

// NewTokenUsageEvent creates a new TokenUsageEvent
func NewTokenUsageEvent(turn int, operation, modelID, provider string, promptTokens, completionTokens, totalTokens int, duration time.Duration, context string) *TokenUsageEvent {
	return &TokenUsageEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		Turn:             turn,
		Operation:        operation,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
		ModelID:          modelID,
		Provider:         provider,
		Duration:         duration,
		Context:          context,
	}
}

// NewTokenUsageEventWithCache creates a new TokenUsageEvent with cache information
func NewTokenUsageEventWithCache(turn int, operation, modelID, provider string, promptTokens, completionTokens, totalTokens int, duration time.Duration, context string, cacheDiscount float64, reasoningTokens int, generationInfo map[string]interface{}) *TokenUsageEvent {
	return &TokenUsageEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		Turn:             turn,
		Operation:        operation,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
		ModelID:          modelID,
		Provider:         provider,
		Duration:         duration,
		Context:          context,
		CacheDiscount:    cacheDiscount,
		ReasoningTokens:  reasoningTokens,
		GenerationInfo:   generationInfo,
	}
}

// SetAgentMode sets the agent mode information on a TokenUsageEvent
func (e *TokenUsageEvent) SetAgentMode(agentMode string, useCodeExecutionMode bool) {
	e.AgentMode = agentMode
	e.UseCodeExecutionMode = useCodeExecutionMode
}

// NewSystemPromptEvent creates a new SystemPromptEvent
func NewSystemPromptEvent(content string, turn int) *SystemPromptEvent {
	return &SystemPromptEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		Content:    content,
		Turn:       turn,
		TokenCount: 0, // Will be set by caller if token counting is available
	}
}

// NewSystemPromptEventWithTokens creates a new SystemPromptEvent with token count
func NewSystemPromptEventWithTokens(content string, turn int, tokenCount int) *SystemPromptEvent {
	return &SystemPromptEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		Content:    content,
		Turn:       turn,
		TokenCount: tokenCount,
	}
}

type RetryAttemptEvent struct {
	BaseEventData
	Turn          int    `json:"turn"`
	AttemptIndex  int    `json:"attempt_index"`
	TotalAttempts int    `json:"total_attempts"`
	ModelID       string `json:"model_id"`
	Provider      string `json:"provider"`
	Phase         string `json:"phase"` // "retry" for a scheduled attempt on the selected model
	Error         string `json:"error,omitempty"`
	Success       bool   `json:"success"`
	Duration      string `json:"duration"`
}

func (e *RetryAttemptEvent) GetEventType() EventType {
	return RetryAttempt
}

func NewRetryAttemptEvent(turn, attemptIndex, totalAttempts int, modelID, provider, phase string, success bool, duration time.Duration, error string) *RetryAttemptEvent {
	return &RetryAttemptEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		Turn:          turn,
		AttemptIndex:  attemptIndex,
		TotalAttempts: totalAttempts,
		ModelID:       modelID,
		Provider:      provider,
		Phase:         phase,
		Success:       success,
		Duration:      duration.String(),
		Error:         error,
	}
}

// MaxTurnsReachedEvent represents when the agent reaches max turns and is given a final chance
type MaxTurnsReachedEvent struct {
	BaseEventData
	Turn         int    `json:"turn"`
	MaxTurns     int    `json:"max_turns"`
	Question     string `json:"question"`
	FinalMessage string `json:"final_message"`
	Duration     string `json:"duration"`
	AgentMode    string `json:"agent_mode"`
}

func (e *MaxTurnsReachedEvent) GetEventType() EventType {
	return MaxTurnsReached
}

// NewMaxTurnsReachedEvent creates a new MaxTurnsReachedEvent
func NewMaxTurnsReachedEvent(turn, maxTurns int, question, finalMessage, agentMode string, duration time.Duration) *MaxTurnsReachedEvent {
	return &MaxTurnsReachedEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		Turn:         turn,
		MaxTurns:     maxTurns,
		Question:     question,
		FinalMessage: finalMessage,
		Duration:     duration.String(),
		AgentMode:    agentMode,
	}
}

// ContextCancelledEvent represents when a conversation is cancelled due to context cancellation
type ContextCancelledEvent struct {
	BaseEventData
	Turn     int           `json:"turn"`
	Reason   string        `json:"reason"`
	Duration time.Duration `json:"duration"`
}

func (e *ContextCancelledEvent) GetEventType() EventType {
	return ContextCancelled
}

func NewContextCancelledEvent(turn int, reason string, duration time.Duration) *ContextCancelledEvent {
	return &ContextCancelledEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		Turn:     turn,
		Reason:   reason,
		Duration: duration,
	}
}

// Unified CacheEvent represents all cache operations across all servers
type CacheEvent struct {
	BaseEventData
	Operation      string `json:"operation"`       // "hit", "miss", "write", "expired", "cleanup", "error", "start"
	ServerName     string `json:"server_name"`     // Server name or "all-servers" for global operations
	CacheKey       string `json:"cache_key"`       // Cache key (optional for some operations)
	ConfigPath     string `json:"config_path"`     // Configuration path
	ToolsCount     int    `json:"tools_count"`     // Number of tools (for hit/write operations)
	DataSize       int64  `json:"data_size"`       // Data size in bytes (for write operations)
	Age            string `json:"age"`             // Age as string (for hit/expired operations)
	TTL            string `json:"ttl"`             // TTL as string (for write/expired operations)
	Reason         string `json:"reason"`          // Reason for miss/expired
	CleanupType    string `json:"cleanup_type"`    // Type of cleanup (for cleanup operations)
	EntriesRemoved int    `json:"entries_removed"` // Entries removed (for cleanup operations)
	EntriesTotal   int    `json:"entries_total"`   // Total entries (for cleanup operations)
	SpaceFreed     int64  `json:"space_freed"`     // Space freed in bytes (for cleanup operations)
	Error          string `json:"error"`           // Error message (for error operations)
	ErrorType      string `json:"error_type"`      // Error type (for error operations)
}

func (e *CacheEvent) GetEventType() EventType {
	return GenericCache
}

// Unified cache event constructors
func NewCacheHitEvent(serverName, cacheKey, configPath string, toolsCount int, age time.Duration) *CacheEvent {
	return &CacheEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		Operation:  "hit",
		ServerName: serverName,
		CacheKey:   cacheKey,
		ConfigPath: configPath,
		ToolsCount: toolsCount,
		Age:        age.String(),
	}
}

func NewCacheOperationStartEvent(serverName, configPath string) *CacheEvent {
	return &CacheEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		Operation:  "start",
		ServerName: serverName,
		ConfigPath: configPath,
	}
}

// ToolExecutionEvent represents tool execution start/end
type ToolExecutionEvent struct {
	BaseEventData
	Turn       int                    `json:"turn"`
	ToolName   string                 `json:"tool_name"`
	ServerName string                 `json:"server_name"`
	ToolCallID string                 `json:"tool_call_id,omitempty"`
	Arguments  map[string]interface{} `json:"arguments,omitempty"`
	Result     string                 `json:"result,omitempty"`
	Duration   time.Duration          `json:"duration,omitempty"`
	Success    bool                   `json:"success,omitempty"`
	Timeout    string                 `json:"timeout,omitempty"`
	Error      string                 `json:"error,omitempty"`
	ErrorType  string                 `json:"error_type,omitempty"`
	Status     string                 `json:"status,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

func (e *ToolExecutionEvent) GetEventType() EventType {
	return ToolExecution
}

// LLMGenerationWithRetryEvent represents LLM generation with retry logic
type LLMGenerationWithRetryEvent struct {
	BaseEventData
	Turn         int                    `json:"turn"`
	MaxRetries   int                    `json:"max_retries"`
	PrimaryModel string                 `json:"primary_model"`
	CurrentLLM   string                 `json:"current_llm"`
	Provider     string                 `json:"provider"`
	Operation    string                 `json:"operation"`
	FinalError   string                 `json:"final_error,omitempty"`
	Usage        map[string]interface{} `json:"usage,omitempty"`
	Status       string                 `json:"status,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

func (e *LLMGenerationWithRetryEvent) GetEventType() EventType {
	return LLMGenerationWithRetry
}

// LLMTextChunkEvent represents a single text chunk from LLM streaming

// UnifiedCompletionEvent represents a standardized completion event for all agent types
type UnifiedCompletionEvent struct {
	BaseEventData
	AgentType   string                 `json:"agent_type"`         // "simple", "react", "orchestrator"
	AgentMode   string                 `json:"agent_mode"`         // "simple", "ReAct", "orchestrator"
	Question    string                 `json:"question"`           // Original user question
	FinalResult string                 `json:"final_result"`       // The final response to show to user
	Status      string                 `json:"status"`             // "completed", "error", "timeout"
	Duration    time.Duration          `json:"duration"`           // Total execution time
	Turns       int                    `json:"turns"`              // Number of conversation turns
	Error       string                 `json:"error,omitempty"`    // Error message if status is error
	Metadata    map[string]interface{} `json:"metadata,omitempty"` // Additional context
}

func (e *UnifiedCompletionEvent) GetEventType() EventType {
	return EventTypeUnifiedCompletion
}

// NewUnifiedCompletionEvent creates a new unified completion event
func NewUnifiedCompletionEvent(agentType, agentMode, question, finalResult, status string, duration time.Duration, turns int) *UnifiedCompletionEvent {
	return &UnifiedCompletionEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		AgentType:   agentType,
		AgentMode:   agentMode,
		Question:    question,
		FinalResult: finalResult,
		Status:      status,
		Duration:    duration,
		Turns:       turns,
		Metadata:    make(map[string]interface{}),
	}
}

// NewUnifiedCompletionEventWithError creates a new unified completion event with error
func NewUnifiedCompletionEventWithError(agentType, agentMode, question, errorMsg string, duration time.Duration, turns int) *UnifiedCompletionEvent {
	event := &UnifiedCompletionEvent{
		BaseEventData: BaseEventData{
			Timestamp: time.Now(),
		},
		AgentType:   agentType,
		AgentMode:   agentMode,
		Question:    question,
		FinalResult: "", // No result for error cases
		Status:      "error",
		Duration:    duration,
		Turns:       turns,
		Error:       errorMsg,
		Metadata:    make(map[string]interface{}),
	}
	event.SetProviderFailureDisplay()
	return event
}

// Note: Orchestrator events have been moved to mcp-agent-builder-go/agent_go/pkg/orchestrator/events/
// This keeps the mcpagent library independent of application-specific orchestrator functionality.

// =============================================================================
// STREAMING EVENTS
// =============================================================================

// StreamingStartEvent represents the start of a streaming response
type StreamingStartEvent struct {
	BaseEventData
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
}

func (e *StreamingStartEvent) GetEventType() EventType {
	return StreamingStart
}

// StreamingChunkEvent represents a single chunk in a streaming response
type StreamingChunkEvent struct {
	BaseEventData
	Content      string `json:"content"`                 // The text content of this chunk
	ChunkIndex   int    `json:"chunk_index"`             // Sequential index of this chunk
	IsToolCall   bool   `json:"is_tool_call"`            // Whether this chunk is part of a tool call
	FinishReason string `json:"finish_reason,omitempty"` // Reason for finishing (if this is the last chunk)
	// Source distinguishes what KIND of chunk this is so a consumer can select
	// the right stream without heuristics:
	//   "transcript" — clean assistant text tailed from the CLI transcript/markers
	//   "content"    — assistant text with no transcript-source marker
	//   "terminal"   — a raw terminal-pane snapshot (ANSI frames), a separate UX
	//                  channel; a design-first / no-terminal UI MUST drop these.
	// A no-terminal UI keeps Source != "terminal"; a raw-terminal UI keeps
	// Source == "terminal". Empty is treated as "content" for backward compat.
	Source string `json:"source,omitempty"`
	// IsDelta reports whether Content is a token-level DELTA (a fragment that may
	// split mid-word, as pi's marker stream emits) rather than a whole assistant
	// text BLOCK (as the transcript-tailing providers emit). A consumer
	// reassembling the message MUST concatenate a run of deltas VERBATIM (never
	// "\n"-join them, which would split tokens) and treat block chunks as
	// separate lines. False for block chunks and terminal snapshots.
	IsDelta bool `json:"is_delta,omitempty"`
}

// StreamingChunkSource constants for StreamingChunkEvent.Source.
const (
	StreamingChunkSourceTranscript = "transcript"
	StreamingChunkSourceContent    = "content"
	StreamingChunkSourceTerminal   = "terminal"
	// StreamingChunkSourcePromptAccepted marks the (empty) chunk a terminal coding CLI sends once it took the prompt.
	StreamingChunkSourcePromptAccepted = "prompt_accepted"
)

func (e *StreamingChunkEvent) GetEventType() EventType {
	return StreamingChunk
}

// StreamingEndEvent represents the end of a streaming response
type StreamingEndEvent struct {
	BaseEventData
	TotalChunks  int    `json:"total_chunks"`
	TotalTokens  int    `json:"total_tokens,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`
	Duration     string `json:"duration,omitempty"`
	// Provider-specific metadata (resolved model, cache stats, etc.)
	ResolvedModel string `json:"resolved_model,omitempty"`
	CacheTokens   int    `json:"cache_tokens,omitempty"`
	ToolCalls     int    `json:"tool_calls,omitempty"`
}

func (e *StreamingEndEvent) GetEventType() EventType {
	return StreamingEnd
}

// StreamingStatusLineEvent represents a statusline telemetry snapshot.
// It carries the full StatusLine payload so no provider telemetry is dropped
// between the adapter and the UI.
type StreamingStatusLineEvent struct {
	BaseEventData
	// ConnectionID is captured by the runtime when this generation starts, never supplied by provider text.
	ConnectionID             string                 `json:"connection_id,omitempty"`
	Provider                 string                 `json:"provider"`
	Model                    string                 `json:"model,omitempty"`
	TmuxSession              string                 `json:"tmux_session,omitempty"` // identifies the owning coding-agent pane so consumers can target it
	InputTokens              int                    `json:"input_tokens,omitempty"`
	OutputTokens             int                    `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int                    `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int                    `json:"cache_read_input_tokens,omitempty"`
	TotalInputTokens         int                    `json:"total_input_tokens,omitempty"`
	TotalOutputTokens        int                    `json:"total_output_tokens,omitempty"`
	CostUSD                  float64                `json:"cost_usd,omitempty"`
	Metadata                 map[string]interface{} `json:"metadata,omitempty"` // raw provider extras (context window, git branch, rate limits, …)
}

func (e *StreamingStatusLineEvent) GetEventType() EventType {
	return StreamingStatusLine
}

// =============================================================================
// MCP SERVER CONNECTION EVENTS
// =============================================================================

// MCPServerConnectionStartEvent represents the start of an MCP server connection
type MCPServerConnectionStartEvent struct {
	BaseEventData
	ServerName string `json:"server_name"`
	ServerURL  string `json:"server_url,omitempty"`
	Protocol   string `json:"protocol,omitempty"` // "sse", "http", "stdio"
}

func (e *MCPServerConnectionStartEvent) GetEventType() EventType {
	return MCPServerConnectionStart
}

// MCPServerConnectionEndEvent represents successful MCP server connection
type MCPServerConnectionEndEvent struct {
	BaseEventData
	ServerName string   `json:"server_name"`
	ToolCount  int      `json:"tool_count,omitempty"`
	ToolNames  []string `json:"tool_names,omitempty"`
	Duration   string   `json:"duration,omitempty"`
}

func (e *MCPServerConnectionEndEvent) GetEventType() EventType {
	return MCPServerConnectionEnd
}

// OTHER MISSING EVENTS
// =============================================================================

// ConversationThinkingEvent represents the agent's thinking/reasoning process
type ConversationThinkingEvent struct {
	BaseEventData
	Thinking string `json:"thinking"` // The thinking/reasoning content
	Turn     int    `json:"turn"`
	// IsDelta marks a fragment of the thinking span still being streamed:
	// append it verbatim to the preceding thinking event of the same turn
	// instead of showing it as a separate block. Providers that emit whole
	// thinking blocks (Claude Code) leave it false; token-streaming ones
	// (Cursor) set it, mirroring StreamingChunkEvent.IsDelta.
	IsDelta bool `json:"is_delta,omitempty"`
}

func (e *ConversationThinkingEvent) GetEventType() EventType {
	return ConversationThinking
}
