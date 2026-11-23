package llmtypes

import (
	"errors"
	"testing"
	"time"
)

// TestInspectorEmitterNilSinkIsNoOp proves the zero-cost path: when
// no sink is wired up, every Emit method returns without touching
// anything. Adapters can therefore call the emitter unconditionally.
func TestInspectorEmitterNilSinkIsNoOp(t *testing.T) {
	em := NewInspectorEmitter(nil, "anthropic", "claude-haiku-4-5")
	if em.Enabled() {
		t.Fatal("Enabled() = true with nil sink; want false")
	}
	// None of these may panic or allocate visible state.
	em.EmitRequest(map[string]interface{}{"x": 1})
	em.EmitEvent("delta", map[string]interface{}{"y": 2})
	em.EmitToolCall(map[string]interface{}{"tool_name": "foo"})
	em.EmitCompletion(map[string]interface{}{"stop_reason": "end_turn"})
	em.EmitError(errors.New("boom"), nil)
}

// TestInspectorEmitterAssignsMonotonicSeq locks in the per-call
// sequence contract: events emitted from a single emitter must carry
// monotonically increasing Seq values starting at 1.
func TestInspectorEmitterAssignsMonotonicSeq(t *testing.T) {
	rec := &InspectorRecorder{}
	em := NewInspectorEmitter(rec, "openai", "gpt-5.4-mini")
	em.EmitRequest(map[string]interface{}{"model": "gpt-5.4-mini"})
	em.EmitEvent("chunk", nil)
	em.EmitEvent("chunk", nil)
	em.EmitToolCall(map[string]interface{}{"tool_name": "echo"})
	em.EmitCompletion(map[string]interface{}{"stop_reason": "stop"})

	events := rec.Events()
	if got, want := len(events), 5; got != want {
		t.Fatalf("recorded %d events, want %d", got, want)
	}
	for i, ev := range events {
		if ev.Seq != i+1 {
			t.Fatalf("events[%d].Seq = %d, want %d", i, ev.Seq, i+1)
		}
	}
}

// TestInspectorEmitterCarriesProviderAndModel proves the per-call
// envelope (Provider + Model) is set on every emitted event without
// the adapter having to repeat it on each Emit call.
func TestInspectorEmitterCarriesProviderAndModel(t *testing.T) {
	rec := &InspectorRecorder{}
	em := NewInspectorEmitter(rec, "vertex", "gemini-3.1-flash-lite-preview")
	em.EmitRequest(nil)
	em.EmitCompletion(nil)

	for _, ev := range rec.Events() {
		if ev.Provider != "vertex" {
			t.Fatalf("event.Provider = %q, want vertex", ev.Provider)
		}
		if ev.Model != "gemini-3.1-flash-lite-preview" {
			t.Fatalf("event.Model = %q, want gemini-3.1-flash-lite-preview", ev.Model)
		}
		if ev.Timestamp.IsZero() {
			t.Fatal("event.Timestamp is zero")
		}
	}
}

// TestInspectorEmitterSetsPhases asserts every helper maps to its
// canonical phase. Trivial, but it's the contract test that fails
// loudly if anyone reorders/renames a constant.
func TestInspectorEmitterSetsPhases(t *testing.T) {
	rec := &InspectorRecorder{}
	em := NewInspectorEmitter(rec, "p", "m")
	em.EmitRequest(nil)
	em.EmitEvent("e", nil)
	em.EmitToolCall(map[string]interface{}{"tool_name": "x"})
	em.EmitCompletion(nil)
	em.EmitError(errors.New("x"), nil)

	want := []InspectorPhase{
		InspectorPhaseRequest,
		InspectorPhaseEvent,
		InspectorPhaseToolCall,
		InspectorPhaseCompletion,
		InspectorPhaseError,
	}
	events := rec.Events()
	for i, ev := range events {
		if ev.Phase != want[i] {
			t.Fatalf("events[%d].Phase = %q, want %q", i, ev.Phase, want[i])
		}
	}
}

// TestInspectorEmitterErrorIncludesMessage proves EmitError stuffs
// the error string into metadata["error"] so consumers don't have to
// know about the err parameter.
func TestInspectorEmitterErrorIncludesMessage(t *testing.T) {
	rec := &InspectorRecorder{}
	em := NewInspectorEmitter(rec, "p", "m")
	em.EmitError(errors.New("rate limited"), map[string]interface{}{"retry_after": 5})

	ev := rec.Events()[0]
	if ev.Metadata["error"] != "rate limited" {
		t.Fatalf("metadata[error] = %v, want rate limited", ev.Metadata["error"])
	}
	if ev.Metadata["retry_after"] != 5 {
		t.Fatalf("retry_after lost during error emission: %v", ev.Metadata["retry_after"])
	}
}

// TestScopedInspectorSinkInjectsStepContext proves the scope decorator
// fills in step identity on every event passing through.
func TestScopedInspectorSinkInjectsStepContext(t *testing.T) {
	rec := &InspectorRecorder{}
	now := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	scope := NewScopedInspectorSink(rec, StepContext{
		StepID:        "fetch-data",
		StepType:      "regular",
		Phase:         "execution",
		StepName:      "Fetch customer data",
		StepIndex:     3,
		StepTotal:     7,
		StepStartedAt: now,
		WorkflowName:  "customer-onboarding",
		SessionID:     "sess-1",
	})

	em := NewInspectorEmitter(scope, "anthropic", "claude-haiku-4-5")
	em.EmitRequest(nil)
	em.EmitCompletion(nil)

	for _, ev := range rec.Events() {
		if ev.StepContext.StepID != "fetch-data" {
			t.Fatalf("StepID = %q, want fetch-data", ev.StepContext.StepID)
		}
		if ev.StepContext.StepIndex != 3 {
			t.Fatalf("StepIndex = %d, want 3", ev.StepContext.StepIndex)
		}
		if !ev.StepContext.StepStartedAt.Equal(now) {
			t.Fatalf("StepStartedAt = %v, want %v", ev.StepContext.StepStartedAt, now)
		}
		if ev.StepContext.WorkflowName != "customer-onboarding" {
			t.Fatalf("WorkflowName missing")
		}
	}
}

// TestScopedInspectorSinkChainingInnerWins proves the merge rule:
// when a deeper (inner) scope sets a field, it overrides the outer.
// This is what lets a sub-call within a step override CallPurpose
// while inheriting the rest of the step envelope.
func TestScopedInspectorSinkChainingInnerWins(t *testing.T) {
	rec := &InspectorRecorder{}
	outer := NewScopedInspectorSink(rec, StepContext{
		StepID:      "step-1",
		StepType:    "regular",
		CallPurpose: "main_generation",
	})
	inner := NewScopedInspectorSink(outer, StepContext{
		CallPurpose: "tool_decision",
	})

	em := NewInspectorEmitter(inner, "openai", "gpt-5.4-mini")
	em.EmitRequest(nil)

	ev := rec.Events()[0]
	if ev.StepContext.StepID != "step-1" {
		t.Fatalf("inherited StepID lost; got %q", ev.StepContext.StepID)
	}
	if ev.StepContext.CallPurpose != "tool_decision" {
		t.Fatalf("inner CallPurpose did not override; got %q", ev.StepContext.CallPurpose)
	}
}

// TestInspectorRecorderConcurrentEmits is a smoke test for the
// recorder's mutex — adapter streaming loops can fire concurrent
// emits and the recorder must not corrupt its slice.
func TestInspectorRecorderConcurrentEmits(t *testing.T) {
	rec := &InspectorRecorder{}
	em := NewInspectorEmitter(rec, "anthropic", "claude-haiku-4-5")

	done := make(chan struct{})
	for g := 0; g < 4; g++ {
		go func() {
			for i := 0; i < 50; i++ {
				em.EmitEvent("delta", nil)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
	if got, want := len(rec.Events()), 200; got != want {
		t.Fatalf("recorded %d events, want %d", got, want)
	}
}
