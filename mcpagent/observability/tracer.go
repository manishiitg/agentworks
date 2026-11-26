package observability

import (
	"time"
)

// TraceID represents a unique identifier for a trace across the entire agent run.
type TraceID string

// SpanID represents a unique identifier for a span inside a trace.
type SpanID string

// UsageMetrics holds token usage information for LLM calls (mirrors Langfuse schema).
// All fields are optional; zero-values mean unavailable.
// Langfuse will automatically calculate costs based on model name and token usage.
type UsageMetrics struct {
	InputTokens  int    `json:"input,omitempty"`
	OutputTokens int    `json:"output,omitempty"`
	TotalTokens  int    `json:"total,omitempty"`
	Unit         string `json:"unit,omitempty"` // e.g. "TOKENS"
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

// Tracer defines the interface for observability tracers
type Tracer interface {
	// EmitEvent emits a generic agent event
	EmitEvent(event AgentEvent) error

	// EmitLLMEvent emits a typed LLM event from providers
	EmitLLMEvent(event LLMEvent) error

	// Trace management methods for Langfuse hierarchy
	StartTrace(name string, input interface{}) TraceID
	EndTrace(traceID TraceID, output interface{})
}

// NoopTracer is a tracer that does nothing
type NoopTracer struct{}

// EmitEvent implements Tracer interface - does nothing
func (n NoopTracer) EmitEvent(event AgentEvent) error {
	return nil
}

// EmitLLMEvent implements Tracer interface - does nothing
func (n NoopTracer) EmitLLMEvent(event LLMEvent) error {
	return nil
}

// StartTrace implements Tracer interface - returns empty trace ID
func (n NoopTracer) StartTrace(name string, input interface{}) TraceID {
	return ""
}

// EndTrace implements Tracer interface - does nothing
func (n NoopTracer) EndTrace(traceID TraceID, output interface{}) {
	// No-op
}
