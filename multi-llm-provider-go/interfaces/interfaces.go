package interfaces

import "time"

// TraceID represents a unique identifier for a trace
type TraceID string

// Tracer defines the interface for observability tracers
// This interface matches observability.Tracer from the main module
type Tracer interface {
	// EmitEvent emits a generic agent event
	EmitEvent(event AgentEvent) error

	// EmitLLMEvent emits a typed LLM event from providers
	EmitLLMEvent(event LLMEvent) error

	// Trace management methods for Langfuse hierarchy
	StartTrace(name string, input interface{}) TraceID
	EndTrace(traceID TraceID, output interface{})
}

// AgentEvent represents an event that can be emitted to tracers
type AgentEvent interface {
	GetType() string
	GetCorrelationID() string
	GetTimestamp() time.Time
	GetData() interface{}
	GetTraceID() string
	GetParentID() string
}

// LLMEvent represents a generic LLM event that can be emitted
type LLMEvent interface {
	GetModelID() string
	GetProvider() string
	GetTimestamp() time.Time
	GetTraceID() string
}

// Logger defines the interface for logging
// Minimal interface with only essential formatted logging methods
type Logger interface {
	// Core logging methods
	Infof(format string, v ...any)
	Errorf(format string, v ...any)
	Debugf(format string, args ...interface{})
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

// EventEmitter defines the interface for emitting LLM events
// The main module will implement this interface to bridge to observability.Tracer
type EventEmitter interface {
	EmitLLMInitializationStart(provider string, modelID string, temperature float64, traceID TraceID, metadata LLMMetadata)
	EmitLLMInitializationSuccess(provider string, modelID string, capabilities string, traceID TraceID, metadata LLMMetadata)
	EmitLLMInitializationError(provider string, modelID string, operation string, err error, traceID TraceID, metadata LLMMetadata)
	EmitLLMGenerationSuccess(provider string, modelID string, operation string, messages int, temperature float64, messageContent string, responseLength int, choicesCount int, traceID TraceID, metadata LLMMetadata)
	EmitLLMGenerationError(provider string, modelID string, operation string, messages int, temperature float64, messageContent string, err error, traceID TraceID, metadata LLMMetadata)
	// Tool call events
	EmitToolCallDetected(provider string, modelID string, toolCallID string, toolName string, arguments string, traceID TraceID, metadata LLMMetadata)
}
