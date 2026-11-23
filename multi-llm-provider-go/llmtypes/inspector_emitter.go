package llmtypes

import (
	"sync"
	"sync/atomic"
	"time"
)

// InspectorEmitter is the per-call helper adapters use to emit
// InspectorEvents. Construct one at the top of GenerateContent with
// NewInspectorEmitter(opts.InspectorSink, provider, model); when the
// sink is nil every Emit method is a single nil-compare and returns.
//
// The struct value is small; carrying a pointer (*InspectorEmitter) in
// longer-lived adapter structs is also fine.
//
// Concurrency: Emit methods are safe to call from multiple goroutines.
// The underlying InspectorSink implementation is responsible for its
// own concurrency safety (the contract in InspectorSink requires it).
//
// Lives in llmtypes (not pkg/adapters/utils) because some adapters
// import pkg/adapters/utils transitively through the model registry
// and the resulting cycle is impossible to break otherwise.
type InspectorEmitter struct {
	sink     InspectorSink
	provider string
	model    string
	seq      atomic.Int64
}

// NewInspectorEmitter builds an emitter that wraps sink. Passing a
// nil sink yields a fully-functional no-op emitter — every method
// returns immediately. Adapters can therefore unconditionally call
// the emitter without guarding each call site.
func NewInspectorEmitter(sink InspectorSink, provider, model string) *InspectorEmitter {
	return &InspectorEmitter{sink: sink, provider: provider, model: model}
}

// Enabled returns true when this emitter has a sink wired up. Useful
// for skipping expensive payload construction (e.g. JSON-marshalling
// a debug payload) before calling Emit.
func (e *InspectorEmitter) Enabled() bool { return e != nil && e.sink != nil }

// EmitRequest fires once at the start of an adapter call.
func (e *InspectorEmitter) EmitRequest(meta map[string]interface{}) {
	if !e.Enabled() {
		return
	}
	e.emit(InspectorPhaseRequest, "", meta)
}

// EmitEvent fires for each provider-side streaming event the adapter
// observes (e.g. anthropic "message_delta", openai delta chunk).
func (e *InspectorEmitter) EmitEvent(eventName string, meta map[string]interface{}) {
	if !e.Enabled() {
		return
	}
	e.emit(InspectorPhaseEvent, eventName, meta)
}

// EmitToolCall fires once per tool selection. meta SHOULD include
// tool_name; tool_call_id and args_length are highly recommended.
func (e *InspectorEmitter) EmitToolCall(meta map[string]interface{}) {
	if !e.Enabled() {
		return
	}
	e.emit(InspectorPhaseToolCall, "", meta)
}

// EmitCompletion fires once on the success path with final
// stop_reason / tokens / cost.
func (e *InspectorEmitter) EmitCompletion(meta map[string]interface{}) {
	if !e.Enabled() {
		return
	}
	e.emit(InspectorPhaseCompletion, "", meta)
}

// EmitError fires once on the failure path. Sensitive substrings
// (API keys) MUST be redacted by the caller.
func (e *InspectorEmitter) EmitError(err error, meta map[string]interface{}) {
	if !e.Enabled() {
		return
	}
	if meta == nil {
		meta = map[string]interface{}{}
	}
	if err != nil {
		meta["error"] = err.Error()
	}
	e.emit(InspectorPhaseError, "", meta)
}

func (e *InspectorEmitter) emit(phase InspectorPhase, eventName string, meta map[string]interface{}) {
	seq := int(e.seq.Add(1))
	event := InspectorEvent{
		Phase:     phase,
		Timestamp: time.Now().UTC(),
		Seq:       seq,
		Provider:  e.provider,
		Model:     e.model,
		EventName: eventName,
		Metadata:  meta,
	}
	e.sink.Emit(event)
}

// ScopedInspectorSink wraps a parent InspectorSink with a fixed
// StepContext, decorating every event passing through with that
// step's identity. The orchestrator uses this at step boundaries to
// attribute adapter-emitted events to the correct workflow step
// without the adapter needing to know about steps at all.
//
// Multiple ScopedInspectorSinks can be chained; the innermost wins
// per-field (see mergeStepContext below).
type ScopedInspectorSink struct {
	parent InspectorSink
	ctx    StepContext
}

// NewScopedInspectorSink builds a scoped sink that injects ctx into
// every event before forwarding to parent.
func NewScopedInspectorSink(parent InspectorSink, ctx StepContext) *ScopedInspectorSink {
	return &ScopedInspectorSink{parent: parent, ctx: ctx}
}

// StepContext returns the scope's recorded step context, merged with
// any enclosing scope. Used by WithObservability to enrich the
// synthetic terminal Header with workflow info (step N/M, attempt,
// agent, parent, triggered_by) so the top of the pane carries the
// same context the inspector timeline already attributes events to.
func (s *ScopedInspectorSink) StepContext() StepContext {
	if s == nil {
		return StepContext{}
	}
	// Walk down to find any enclosing scoped sink and merge.
	if parent, ok := s.parent.(*ScopedInspectorSink); ok {
		return mergeStepContext(s.ctx, parent.StepContext())
	}
	return s.ctx
}

// InspectorSinkStepContext extracts the step context attached to any
// InspectorSink, returning a zero StepContext if the sink doesn't
// carry one (e.g. a bare InspectorRecorder used in tests).
func InspectorSinkStepContext(sink InspectorSink) StepContext {
	if scoped, ok := sink.(*ScopedInspectorSink); ok {
		return scoped.StepContext()
	}
	return StepContext{}
}

// Emit implements InspectorSink. Zero-valued fields on the incoming
// event are filled in from the scope; non-zero fields are preserved
// (this is what lets a chained inner scope override an outer one).
func (s *ScopedInspectorSink) Emit(event InspectorEvent) {
	if s == nil || s.parent == nil {
		return
	}
	event.StepContext = mergeStepContext(event.StepContext, s.ctx)
	s.parent.Emit(event)
}

func mergeStepContext(child, parent StepContext) StepContext {
	if child.StepID == "" {
		child.StepID = parent.StepID
	}
	if child.StepType == "" {
		child.StepType = parent.StepType
	}
	if child.Phase == "" {
		child.Phase = parent.Phase
	}
	if child.ExecutionOwnerID == "" {
		child.ExecutionOwnerID = parent.ExecutionOwnerID
	}
	if child.StepName == "" {
		child.StepName = parent.StepName
	}
	if child.StepIndex == 0 {
		child.StepIndex = parent.StepIndex
	}
	if child.StepTotal == 0 {
		child.StepTotal = parent.StepTotal
	}
	if child.StepStartedAt.IsZero() {
		child.StepStartedAt = parent.StepStartedAt
	}
	if child.AgentName == "" {
		child.AgentName = parent.AgentName
	}
	if child.Attempt == 0 {
		child.Attempt = parent.Attempt
	}
	if child.CallPurpose == "" {
		child.CallPurpose = parent.CallPurpose
	}
	if child.BatchGroupName == "" {
		child.BatchGroupName = parent.BatchGroupName
	}
	if child.BatchGroupIndex == 0 {
		child.BatchGroupIndex = parent.BatchGroupIndex
	}
	if child.BatchTotalGroups == 0 {
		child.BatchTotalGroups = parent.BatchTotalGroups
	}
	if child.ParentStepID == "" {
		child.ParentStepID = parent.ParentStepID
	}
	if child.WorkflowRunID == "" {
		child.WorkflowRunID = parent.WorkflowRunID
	}
	if child.WorkflowName == "" {
		child.WorkflowName = parent.WorkflowName
	}
	if child.SessionID == "" {
		child.SessionID = parent.SessionID
	}
	if child.UserID == "" {
		child.UserID = parent.UserID
	}
	return child
}

// InspectorRecorder is a fixture-friendly sink that captures all
// emitted events into an in-memory slice. Tests and the matrix
// contract assert against the recorded events; also useful for
// instrumentation paths that want the full event log before deciding
// what to keep.
//
// Concurrency-safe; events appear in arrival order.
type InspectorRecorder struct {
	mu     sync.Mutex
	events []InspectorEvent
}

// Emit implements InspectorSink.
func (r *InspectorRecorder) Emit(event InspectorEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

// Events returns a snapshot copy of the recorded events. Safe to
// iterate without holding any lock.
func (r *InspectorRecorder) Events() []InspectorEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]InspectorEvent, len(r.events))
	copy(out, r.events)
	return out
}
