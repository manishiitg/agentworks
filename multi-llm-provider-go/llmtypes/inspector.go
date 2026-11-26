package llmtypes

import "time"

// InspectorSink is the consumer of structured debug events emitted by
// LLM adapters. It is intentionally separate from the streaming
// content channel (StreamChan) so the inspector path stays opt-in:
// when no sink is attached, adapters skip emission entirely and pay
// zero cost. When a user opens the debug panel for a session, the
// orchestrator wires a sink up; events flow into mcp-agent-builder-go's
// inspector store and out through GET /api/inspector/<session>.
//
// Implementations MUST be safe for concurrent Emit calls — a single
// adapter call can fire events from multiple goroutines (streaming
// reader + tool-call decoder + error path).
type InspectorSink interface {
	Emit(event InspectorEvent)
}

// InspectorPhase is the high-level bucket every InspectorEvent falls
// into. The set is closed: adding a new phase is a breaking change.
type InspectorPhase string

const (
	// InspectorPhaseRequest fires once per adapter call, immediately
	// before the HTTP request is dispatched. Captures the call envelope
	// (model, params, message count, tool count, system size).
	InspectorPhaseRequest InspectorPhase = "request"

	// InspectorPhaseEvent fires for each notable provider-side event
	// during streaming. EventName carries the provider-specific name
	// (e.g. "message_delta", "content_block_stop", "stream_end").
	// Metadata is summary-only — no full content unless the caller
	// opted into debug_full_payload.
	InspectorPhaseEvent InspectorPhase = "event"

	// InspectorPhaseToolCall fires when the model selects a tool. One
	// event per tool call. Metadata carries tool_name + summary.
	InspectorPhaseToolCall InspectorPhase = "tool_call"

	// InspectorPhaseCompletion fires once per adapter call on the
	// success path with final usage/cost/stop_reason. Mirror to
	// InspectorPhaseError on failure.
	InspectorPhaseCompletion InspectorPhase = "completion"

	// InspectorPhaseError fires once per adapter call on the failure
	// path. The terminating event for any errored call.
	InspectorPhaseError InspectorPhase = "error"
)

// InspectorEvent is the unified, provider-agnostic representation of
// a notable moment in an LLM call's lifecycle. Every adapter that
// participates in the inspector contract MUST be able to produce the
// same shape regardless of whether the underlying transport is HTTP
// streaming (anthropic/openai/vertex), JSON-over-stdio (claude-code
// --print, codex --exec, etc.), or a fixture replay.
type InspectorEvent struct {
	Phase     InspectorPhase
	Timestamp time.Time

	// Seq is a monotonic counter scoped to a single adapter call.
	// Restarts at 1 for each GenerateContent invocation. Lets the
	// inspector UI render an ordered timeline even if chunks arrive
	// out-of-order.
	Seq int

	// Provider identifies the adapter: "anthropic", "openai",
	// "vertex", "claudecode", "codex", "gemini", "cursor", etc.
	Provider string

	// Model is the model ID this call targets. Effective model
	// (when it differs from the requested alias) shows up under
	// Metadata["effective_model"] only after the completion event.
	Model string

	// EventName is the provider-specific event tag. Only meaningful
	// for Phase=Event. Empty for the others.
	EventName string

	// Metadata is the per-event payload. Required-key conventions per
	// phase are documented in docs/inspector_contract.md. Keys MUST
	// be JSON-marshalable. Sensitive values (API keys, full message
	// bodies) MUST NOT appear unless the caller opted in via
	// debug_full_payload.
	Metadata map[string]interface{}

	// StepContext attributes this event to a specific workflow step
	// when emitted through a scoped sink (see InspectorScopedSink in
	// pkg/adapters/utils). Empty when the call originates outside a
	// workflow (e.g. plain chat).
	StepContext StepContext
}

// StepContext is the orchestrator-side context attached to every
// inspector event when emitted through a scoped sink. Required fields
// (StepID, StepType, Phase) are populated by the workflow executor at
// step boundary; optional fields fill in as available.
type StepContext struct {
	// Required for in-workflow events
	StepID   string `json:"step_id,omitempty"`
	StepType string `json:"step_type,omitempty"`
	Phase    string `json:"phase,omitempty"`

	// Required for parallel-batch disambiguation
	ExecutionOwnerID string `json:"execution_owner_id,omitempty"`

	// Per-step UX context
	StepName      string    `json:"step_name,omitempty"`
	StepIndex     int       `json:"step_index,omitempty"`
	StepTotal     int       `json:"step_total,omitempty"`
	StepStartedAt time.Time `json:"step_started_at,omitempty"`
	AgentName     string    `json:"agent_name,omitempty"`
	Attempt       int       `json:"attempt,omitempty"`
	CallPurpose   string    `json:"call_purpose,omitempty"`

	// Batch parallelism
	BatchGroupName   string `json:"batch_group_name,omitempty"`
	BatchGroupIndex  int    `json:"batch_group_index,omitempty"`
	BatchTotalGroups int    `json:"batch_total_groups,omitempty"`

	// Workflow-level
	ParentStepID  string `json:"parent_step_id,omitempty"`
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
	WorkflowName  string `json:"workflow_name,omitempty"`

	// Session/user pulled from the request context when available.
	SessionID string `json:"session_id,omitempty"`
	UserID    string `json:"user_id,omitempty"`
}

// InspectorSinkFunc is a convenience adapter that turns a plain
// function into an InspectorSink. Useful for tests and one-off
// instrumentation.
type InspectorSinkFunc func(event InspectorEvent)

// Emit implements InspectorSink.
func (f InspectorSinkFunc) Emit(event InspectorEvent) { f(event) }
