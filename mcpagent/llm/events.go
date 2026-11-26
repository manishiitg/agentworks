package llm

import (
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/mcpagent/observability"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// LLM Event Types - Constants for event type names
const (
	EventTypeLLMInitializationStart   = "llm_initialization_start"
	EventTypeLLMInitializationSuccess = "llm_initialization_success"
	EventTypeLLMInitializationError   = "llm_initialization_error"
	EventTypeLLMGenerationStart       = "llm_generation_start"
	EventTypeLLMGenerationSuccess     = "llm_generation_success"
	EventTypeLLMGenerationError       = "llm_generation_error"
	EventTypeLLMToolCallDetected      = "llm_tool_call_detected"
)

// LLM Operation Types - Constants for operation names
const (
	OperationLLMInitialization = "llm_initialization"
	OperationLLMGeneration     = "llm_generation"
	OperationLLMToolCalling    = "llm_tool_calling"
)

// LLM Status Types - Constants for status values
const (
	StatusLLMInitialized = "initialized"
	StatusLLMFailed      = "failed"
	StatusLLMSuccess     = "success"
	StatusLLMInProgress  = "in_progress"
)

// LLM Capabilities - Constants for capability strings
const (
	CapabilityTextGeneration = "text_generation"
	CapabilityToolCalling    = "tool_calling"
	CapabilityStreaming      = "streaming"
)

// TokenUsage represents token consumption information
type TokenUsage struct {
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
	TotalTokens  int    `json:"total_tokens,omitempty"`
	Unit         string `json:"unit,omitempty"`
	Cost         string `json:"cost,omitempty"`
}

// LLMMetadata represents common metadata for LLM events
type LLMMetadata struct {
	ModelVersion     string            `json:"model_version,omitempty"`
	MaxTokens        int               `json:"max_tokens,omitempty"`
	TopP             float64           `json:"top_p,omitempty"`
	FrequencyPenalty float64           `json:"frequency_penalty,omitempty"`
	PresencePenalty  float64           `json:"presence_penalty,omitempty"`
	StopSequences    []string          `json:"stop_sequences,omitempty"`
	User             string            `json:"user,omitempty"`
	CustomFields     map[string]string `json:"custom_fields,omitempty"`
}

// LLMInitializationStartEvent represents the start of LLM initialization
type LLMInitializationStartEvent struct {
	ModelID     string      `json:"model_id"`
	Temperature float64     `json:"temperature"`
	Provider    string      `json:"provider"`
	Operation   string      `json:"operation"`
	Timestamp   time.Time   `json:"timestamp"`
	TraceID     string      `json:"trace_id"`
	Metadata    LLMMetadata `json:"metadata,omitempty"`
}

// GetModelID returns the model ID
func (e *LLMInitializationStartEvent) GetModelID() string { return e.ModelID }

// GetProvider returns the provider name
func (e *LLMInitializationStartEvent) GetProvider() string { return e.Provider }

// GetTimestamp returns the event timestamp
func (e *LLMInitializationStartEvent) GetTimestamp() time.Time { return e.Timestamp }

// GetTraceID returns the trace ID
func (e *LLMInitializationStartEvent) GetTraceID() string { return e.TraceID }

// LLMInitializationSuccessEvent represents successful LLM initialization
type LLMInitializationSuccessEvent struct {
	ModelID      string      `json:"model_id"`
	Provider     string      `json:"provider"`
	Status       string      `json:"status"`
	Capabilities []string    `json:"capabilities"`
	Timestamp    time.Time   `json:"timestamp"`
	TraceID      string      `json:"trace_id"`
	Metadata     LLMMetadata `json:"metadata,omitempty"`
}

// GetModelID returns the model ID
func (e *LLMInitializationSuccessEvent) GetModelID() string { return e.ModelID }

// GetProvider returns the provider name
func (e *LLMInitializationSuccessEvent) GetProvider() string { return e.Provider }

// GetTimestamp returns the event timestamp
func (e *LLMInitializationSuccessEvent) GetTimestamp() time.Time { return e.Timestamp }

// GetTraceID returns the trace ID
func (e *LLMInitializationSuccessEvent) GetTraceID() string { return e.TraceID }

// LLMInitializationErrorEvent represents failed LLM initialization
type LLMInitializationErrorEvent struct {
	ModelID   string      `json:"model_id"`
	Provider  string      `json:"provider"`
	Operation string      `json:"operation"`
	Error     string      `json:"error"`
	ErrorType string      `json:"error_type"`
	Status    string      `json:"status"`
	Timestamp time.Time   `json:"timestamp"`
	TraceID   string      `json:"trace_id"`
	Metadata  LLMMetadata `json:"metadata,omitempty"`
}

// GetModelID returns the model ID
func (e *LLMInitializationErrorEvent) GetModelID() string { return e.ModelID }

// GetProvider returns the provider name
func (e *LLMInitializationErrorEvent) GetProvider() string { return e.Provider }

// GetTimestamp returns the event timestamp
func (e *LLMInitializationErrorEvent) GetTimestamp() time.Time { return e.Timestamp }

// GetTraceID returns the trace ID
func (e *LLMInitializationErrorEvent) GetTraceID() string { return e.TraceID }

// LLMGenerationStartEvent represents the start of LLM generation
type LLMGenerationStartEvent struct {
	ModelID        string      `json:"model_id"`
	Provider       string      `json:"provider"`
	Operation      string      `json:"operation"`
	Messages       int         `json:"messages"`
	Temperature    float64     `json:"temperature"`
	MessageContent string      `json:"message_content"`
	Timestamp      time.Time   `json:"timestamp"`
	TraceID        string      `json:"trace_id"`
	Metadata       LLMMetadata `json:"metadata,omitempty"`
}

// GetModelID returns the model ID
func (e *LLMGenerationStartEvent) GetModelID() string { return e.ModelID }

// GetProvider returns the provider name
func (e *LLMGenerationStartEvent) GetProvider() string { return e.Provider }

// GetTimestamp returns the event timestamp
func (e *LLMGenerationStartEvent) GetTimestamp() time.Time { return e.Timestamp }

// GetTraceID returns the trace ID
func (e *LLMGenerationStartEvent) GetTraceID() string { return e.TraceID }

// LLMGenerationSuccessEvent represents successful LLM generation
type LLMGenerationSuccessEvent struct {
	ModelID        string      `json:"model_id"`
	Provider       string      `json:"provider"`
	Operation      string      `json:"operation"`
	Messages       int         `json:"messages"`
	Temperature    float64     `json:"temperature"`
	MessageContent string      `json:"message_content"`
	ResponseLength int         `json:"response_length"`
	ChoicesCount   int         `json:"choices_count"`
	TokenUsage     TokenUsage  `json:"token_usage,omitempty"`
	Timestamp      time.Time   `json:"timestamp"`
	TraceID        string      `json:"trace_id"`
	Metadata       LLMMetadata `json:"metadata,omitempty"`
}

// GetModelID returns the model ID
func (e *LLMGenerationSuccessEvent) GetModelID() string { return e.ModelID }

// GetProvider returns the provider name
func (e *LLMGenerationSuccessEvent) GetProvider() string { return e.Provider }

// GetTimestamp returns the event timestamp
func (e *LLMGenerationSuccessEvent) GetTimestamp() time.Time { return e.Timestamp }

// GetTraceID returns the trace ID
func (e *LLMGenerationSuccessEvent) GetTraceID() string { return e.TraceID }

// LLMGenerationErrorEvent represents failed LLM generation
type LLMGenerationErrorEvent struct {
	ModelID        string      `json:"model_id"`
	Provider       string      `json:"provider"`
	Operation      string      `json:"operation"`
	Messages       int         `json:"messages"`
	Temperature    float64     `json:"temperature"`
	MessageContent string      `json:"message_content"`
	Error          string      `json:"error"`
	ErrorType      string      `json:"error_type"`
	Timestamp      time.Time   `json:"timestamp"`
	TraceID        string      `json:"trace_id"`
	Metadata       LLMMetadata `json:"metadata,omitempty"`
}

// GetModelID returns the model ID
func (e *LLMGenerationErrorEvent) GetModelID() string { return e.ModelID }

// GetProvider returns the provider name
func (e *LLMGenerationErrorEvent) GetProvider() string { return e.Provider }

// GetTimestamp returns the event timestamp
func (e *LLMGenerationErrorEvent) GetTimestamp() time.Time { return e.Timestamp }

// GetTraceID returns the trace ID
func (e *LLMGenerationErrorEvent) GetTraceID() string { return e.TraceID }

// LLMToolCallDetectedEvent represents a tool call detected in LLM response
type LLMToolCallDetectedEvent struct {
	ModelID    string      `json:"model_id"`
	Provider   string      `json:"provider"`
	Operation  string      `json:"operation"`
	ToolCallID string      `json:"tool_call_id"`
	ToolName   string      `json:"tool_name"`
	Arguments  string      `json:"arguments"`
	Timestamp  time.Time   `json:"timestamp"`
	TraceID    string      `json:"trace_id"`
	Metadata   LLMMetadata `json:"metadata,omitempty"`
}

// GetModelID returns the model ID
func (e *LLMToolCallDetectedEvent) GetModelID() string { return e.ModelID }

// GetProvider returns the provider name
func (e *LLMToolCallDetectedEvent) GetProvider() string { return e.Provider }

// GetTimestamp returns the event timestamp
func (e *LLMToolCallDetectedEvent) GetTimestamp() time.Time { return e.Timestamp }

// GetTraceID returns the trace ID
func (e *LLMToolCallDetectedEvent) GetTraceID() string { return e.TraceID }

// emitLLMInitializationStart emits a typed start event for LLM initialization
func emitLLMInitializationStart(tracers []observability.Tracer, provider string, modelID string, temperature float64, traceID observability.TraceID, metadata LLMMetadata) {
	if len(tracers) == 0 {
		return
	}

	event := &LLMInitializationStartEvent{
		ModelID:     modelID,
		Temperature: temperature,
		Provider:    provider,
		Operation:   OperationLLMInitialization,
		Timestamp:   time.Now(),
		TraceID:     string(traceID),
		Metadata:    metadata,
	}

	for _, tracer := range tracers {
		if err := tracer.EmitLLMEvent(event); err != nil {
			// Log error but continue with other tracers
			continue
		}
	}
}

// emitLLMInitializationSuccess emits a typed success event for LLM initialization
func emitLLMInitializationSuccess(tracers []observability.Tracer, provider string, modelID string, capabilities string, traceID observability.TraceID, metadata LLMMetadata) {
	if len(tracers) == 0 {
		return
	}

	// Convert capabilities string to slice
	capabilitiesSlice := strings.Split(capabilities, ",")
	for i, cap := range capabilitiesSlice {
		capabilitiesSlice[i] = strings.TrimSpace(cap)
	}

	event := &LLMInitializationSuccessEvent{
		ModelID:      modelID,
		Provider:     provider,
		Status:       StatusLLMInitialized,
		Capabilities: capabilitiesSlice,
		Timestamp:    time.Now(),
		TraceID:      string(traceID),
		Metadata:     metadata,
	}

	for _, tracer := range tracers {
		if err := tracer.EmitLLMEvent(event); err != nil {
			// Log error but continue with other tracers
			continue
		}
	}
}

// emitLLMInitializationError emits a typed error event for LLM initialization
func emitLLMInitializationError(tracers []observability.Tracer, provider string, modelID string, operation string, err error, traceID observability.TraceID, metadata LLMMetadata) {
	if len(tracers) == 0 {
		return
	}

	event := &LLMInitializationErrorEvent{
		ModelID:   modelID,
		Provider:  provider,
		Operation: operation,
		Error:     err.Error(),
		ErrorType: fmt.Sprintf("%T", err),
		Status:    StatusLLMFailed,
		Timestamp: time.Now(),
		TraceID:   string(traceID),
		Metadata:  metadata,
	}

	for _, tracer := range tracers {
		if err := tracer.EmitLLMEvent(event); err != nil {
			// Log error but continue with other tracers
			continue
		}
	}
}

// emitLLMGenerationSuccess emits a typed success event for LLM generation
func emitLLMGenerationSuccess(tracers []observability.Tracer, provider string, modelID string, operation string, messages int, temperature float64, messageContent string, responseLength int, choicesCount int, traceID observability.TraceID, metadata LLMMetadata) {
	if len(tracers) == 0 {
		return
	}

	// Extract token usage from metadata if available
	var tokenUsage TokenUsage
	if metadata.CustomFields != nil {
		if _, ok := metadata.CustomFields["generation_info"]; ok {
			// For now, we'll create a simple TokenUsage structure
			// In the future, we could enhance this to parse more complex token usage data
			tokenUsage = TokenUsage{
				Unit: "TOKENS",
			}
		}
	}

	event := &LLMGenerationSuccessEvent{
		ModelID:        modelID,
		Provider:       provider,
		Operation:      operation,
		Messages:       messages,
		Temperature:    temperature,
		MessageContent: messageContent,
		ResponseLength: responseLength,
		ChoicesCount:   choicesCount,
		TokenUsage:     tokenUsage,
		Timestamp:      time.Now(),
		TraceID:        string(traceID),
		Metadata:       metadata,
	}

	for _, tracer := range tracers {
		if err := tracer.EmitLLMEvent(event); err != nil {
			// Log error but continue with other tracers
			continue
		}
	}
}

// emitLLMGenerationError emits a typed error event for LLM generation
func emitLLMGenerationError(tracers []observability.Tracer, provider string, modelID string, operation string, messages int, temperature float64, messageContent string, err error, traceID observability.TraceID, metadata LLMMetadata) {
	if len(tracers) == 0 {
		return
	}

	event := &LLMGenerationErrorEvent{
		ModelID:        modelID,
		Provider:       provider,
		Operation:      operation,
		Messages:       messages,
		Temperature:    temperature,
		MessageContent: messageContent,
		Error:          err.Error(),
		ErrorType:      fmt.Sprintf("%T", err),
		Timestamp:      time.Now(),
		TraceID:        string(traceID),
		Metadata:       metadata,
	}

	for _, tracer := range tracers {
		if err := tracer.EmitLLMEvent(event); err != nil {
			// Log error but continue with other tracers
			continue
		}
	}
}

// emitLLMToolCallDetected emits a typed event for tool call detection
func emitLLMToolCallDetected(tracers []observability.Tracer, provider string, modelID string, toolCallID string, toolName string, arguments string, traceID observability.TraceID, metadata LLMMetadata) {
	if len(tracers) == 0 {
		return
	}

	event := &LLMToolCallDetectedEvent{
		ModelID:    modelID,
		Provider:   provider,
		Operation:  OperationLLMToolCalling,
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Arguments:  arguments,
		Timestamp:  time.Now(),
		TraceID:    string(traceID),
		Metadata:   metadata,
	}

	for _, tracer := range tracers {
		if err := tracer.EmitLLMEvent(event); err != nil {
			// Log error but continue with other tracers
			continue
		}
	}
}

// extractTokenUsageFromGenerationInfo extracts token usage from GenerationInfo
func extractTokenUsageFromGenerationInfo(generationInfo *llmtypes.GenerationInfo) observability.UsageMetrics {
	usage := observability.UsageMetrics{Unit: "TOKENS"}

	if generationInfo == nil {
		return usage
	}

	// Extract input tokens (check multiple naming conventions in priority order)
	if generationInfo.InputTokens != nil {
		usage.InputTokens = *generationInfo.InputTokens
	} else if generationInfo.InputTokensCap != nil {
		usage.InputTokens = *generationInfo.InputTokensCap
	} else if generationInfo.PromptTokens != nil {
		usage.InputTokens = *generationInfo.PromptTokens
	} else if generationInfo.PromptTokensCap != nil {
		usage.InputTokens = *generationInfo.PromptTokensCap
	}

	// Extract output tokens (check multiple naming conventions in priority order)
	if generationInfo.OutputTokens != nil {
		usage.OutputTokens = *generationInfo.OutputTokens
	} else if generationInfo.OutputTokensCap != nil {
		usage.OutputTokens = *generationInfo.OutputTokensCap
	} else if generationInfo.CompletionTokens != nil {
		usage.OutputTokens = *generationInfo.CompletionTokens
	} else if generationInfo.CompletionTokensCap != nil {
		usage.OutputTokens = *generationInfo.CompletionTokensCap
	}

	// Extract total tokens (check multiple naming conventions in priority order)
	if generationInfo.TotalTokens != nil {
		usage.TotalTokens = *generationInfo.TotalTokens
	} else if generationInfo.TotalTokensCap != nil {
		usage.TotalTokens = *generationInfo.TotalTokensCap
	}

	// Calculate total tokens if not provided by the provider
	if usage.TotalTokens == 0 && usage.InputTokens > 0 && usage.OutputTokens > 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}

	return usage
}

// ExtractTokenUsageWithCacheInfo extracts token usage with cache information from ContentResponse.
// It prioritizes the unified Usage field, falling back to GenerationInfo if needed.
// Returns: (usageMetrics, cacheDiscount, reasoningTokens, thoughtsTokens, cacheTokens, generationInfoMap)
func ExtractTokenUsageWithCacheInfo(resp *llmtypes.ContentResponse) (observability.UsageMetrics, float64, int, int, int, map[string]interface{}) {
	var usage observability.UsageMetrics
	var cacheDiscount float64
	var reasoningTokens int
	var thoughtsTokens int
	var cacheTokens int
	var infoCopy map[string]interface{}

	// Priority 1: Use unified Usage field (if available)
	if resp != nil && resp.Usage != nil {
		usage = observability.UsageMetrics{
			Unit:         "TOKENS",
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
			TotalTokens:  resp.Usage.TotalTokens,
		}

		// Extract reasoning tokens from unified Usage
		if resp.Usage.ReasoningTokens != nil {
			reasoningTokens = *resp.Usage.ReasoningTokens
		}

		// Extract thoughts tokens from unified Usage
		if resp.Usage.ThoughtsTokens != nil {
			thoughtsTokens = *resp.Usage.ThoughtsTokens
		}

		// Extract cache tokens from unified Usage
		if resp.Usage.CacheTokens != nil {
			cacheTokens = *resp.Usage.CacheTokens
		}

		// Still need to get cache discount from GenerationInfo (not in Usage)
		if len(resp.Choices) > 0 && resp.Choices[0].GenerationInfo != nil {
			genInfo := resp.Choices[0].GenerationInfo
			if genInfo.CacheDiscount != nil {
				cacheDiscount = *genInfo.CacheDiscount
			}
		}
	}

	// Priority 2: Fall back to GenerationInfo (for backward compatibility)
	if resp != nil && len(resp.Choices) > 0 && resp.Choices[0].GenerationInfo != nil {
		generationInfo := resp.Choices[0].GenerationInfo

		// If we didn't get usage from unified field, extract from GenerationInfo
		if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.TotalTokens == 0 {
			usage = extractTokenUsageFromGenerationInfo(generationInfo)
		}

		// Extract cache discount (if not already set)
		if cacheDiscount == 0 && generationInfo.CacheDiscount != nil {
			cacheDiscount = *generationInfo.CacheDiscount
		}

		// Extract reasoning tokens (if not already set from Usage)
		if reasoningTokens == 0 && generationInfo.ReasoningTokens != nil {
			reasoningTokens = *generationInfo.ReasoningTokens
		}

		// Extract thoughts tokens (if not already set from Usage)
		if thoughtsTokens == 0 && generationInfo.ThoughtsTokens != nil {
			thoughtsTokens = *generationInfo.ThoughtsTokens
		}

		// Extract cache tokens from GenerationInfo (if not already set from Usage)
		// Use unified extraction from multi-llm-provider-go
		if cacheTokens == 0 {
			usage := llmtypes.ExtractUsageFromGenerationInfo(generationInfo)
			if usage != nil && usage.CacheTokens != nil {
				cacheTokens = *usage.CacheTokens
			}
		}

		// Create a copy of generationInfo for logging (convert to map for backward compatibility)
		infoCopy = make(map[string]interface{})
		if generationInfo.InputTokens != nil {
			infoCopy["input_tokens"] = *generationInfo.InputTokens
		}
		if generationInfo.OutputTokens != nil {
			infoCopy["output_tokens"] = *generationInfo.OutputTokens
		}
		if generationInfo.TotalTokens != nil {
			infoCopy["total_tokens"] = *generationInfo.TotalTokens
		}
		if generationInfo.CacheDiscount != nil {
			infoCopy["cache_discount"] = *generationInfo.CacheDiscount
		}
		if generationInfo.ReasoningTokens != nil {
			infoCopy["ReasoningTokens"] = *generationInfo.ReasoningTokens
		}
		if generationInfo.ThoughtsTokens != nil {
			infoCopy["ThoughtsTokens"] = *generationInfo.ThoughtsTokens
		}
		if generationInfo.CachedContentTokens != nil {
			infoCopy["CachedContentTokens"] = *generationInfo.CachedContentTokens
		}
		// Add any additional fields from the Additional map
		for k, v := range generationInfo.Additional {
			infoCopy[k] = v
		}
	}

	return usage, cacheDiscount, reasoningTokens, thoughtsTokens, cacheTokens, infoCopy
}

// EventEmitterAdapter implements multi-llm-provider-go EventEmitter interface
// and bridges events to the existing observability tracers
type EventEmitterAdapter struct {
	tracers []observability.Tracer
}

// NewEventEmitterAdapter creates a new adapter that implements multi-llm-provider-go EventEmitter
func NewEventEmitterAdapter(tracers []observability.Tracer) *EventEmitterAdapter {
	return &EventEmitterAdapter{
		tracers: tracers,
	}
}

// convertMetadata converts llm-providers metadata to internal LLMMetadata
func convertMetadata(metadata llmproviders.LLMMetadata) LLMMetadata {
	return LLMMetadata{
		ModelVersion:     metadata.ModelVersion,
		MaxTokens:        metadata.MaxTokens,
		TopP:             metadata.TopP,
		FrequencyPenalty: metadata.FrequencyPenalty,
		PresencePenalty:  metadata.PresencePenalty,
		StopSequences:    metadata.StopSequences,
		User:             metadata.User,
		CustomFields:     metadata.CustomFields,
	}
}

// EmitLLMInitializationStart implements llm-providers EventEmitter interface
func (e *EventEmitterAdapter) EmitLLMInitializationStart(provider string, modelID string, temperature float64, traceID interfaces.TraceID, metadata llmproviders.LLMMetadata) {
	internalMetadata := convertMetadata(metadata)
	emitLLMInitializationStart(e.tracers, provider, modelID, temperature, observability.TraceID(traceID), internalMetadata)
}

// EmitLLMInitializationSuccess implements llm-providers EventEmitter interface
func (e *EventEmitterAdapter) EmitLLMInitializationSuccess(provider string, modelID string, capabilities string, traceID interfaces.TraceID, metadata llmproviders.LLMMetadata) {
	internalMetadata := convertMetadata(metadata)
	emitLLMInitializationSuccess(e.tracers, provider, modelID, capabilities, observability.TraceID(traceID), internalMetadata)
}

// EmitLLMInitializationError implements llm-providers EventEmitter interface
func (e *EventEmitterAdapter) EmitLLMInitializationError(provider string, modelID string, operation string, err error, traceID interfaces.TraceID, metadata llmproviders.LLMMetadata) {
	internalMetadata := convertMetadata(metadata)
	emitLLMInitializationError(e.tracers, provider, modelID, operation, err, observability.TraceID(traceID), internalMetadata)
}

// EmitLLMGenerationSuccess implements llm-providers EventEmitter interface
func (e *EventEmitterAdapter) EmitLLMGenerationSuccess(provider string, modelID string, operation string, messages int, temperature float64, messageContent string, responseLength int, choicesCount int, traceID interfaces.TraceID, metadata llmproviders.LLMMetadata) {
	internalMetadata := convertMetadata(metadata)
	emitLLMGenerationSuccess(e.tracers, provider, modelID, operation, messages, temperature, messageContent, responseLength, choicesCount, observability.TraceID(traceID), internalMetadata)
}

// EmitLLMGenerationError implements llm-providers EventEmitter interface
func (e *EventEmitterAdapter) EmitLLMGenerationError(provider string, modelID string, operation string, messages int, temperature float64, messageContent string, err error, traceID interfaces.TraceID, metadata llmproviders.LLMMetadata) {
	internalMetadata := convertMetadata(metadata)
	emitLLMGenerationError(e.tracers, provider, modelID, operation, messages, temperature, messageContent, err, observability.TraceID(traceID), internalMetadata)
}

// EmitToolCallDetected implements llm-providers EventEmitter interface
func (e *EventEmitterAdapter) EmitToolCallDetected(provider string, modelID string, toolCallID string, toolName string, arguments string, traceID interfaces.TraceID, metadata llmproviders.LLMMetadata) {
	internalMetadata := convertMetadata(metadata)
	emitLLMToolCallDetected(e.tracers, provider, modelID, toolCallID, toolName, arguments, observability.TraceID(traceID), internalMetadata)
}
