package anthropic

import (
	"context"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestAnthropicRealInspectorContract is the per-adapter regression
// for the inspector emission contract. It makes a real API call with
// an attached InspectorRecorder and asserts the emitted event stream
// follows the canonical shape:
//
//   1. exactly one request event (first, monotonic Seq=1)
//   2. one or more event events with provider-specific names
//   3. zero or more tool_call events (this prompt forces none)
//   4. exactly one completion event (last)
//   5. no error event
//   6. every event carries Provider="anthropic" and Model
//   7. Seq is monotonically increasing without gaps
//   8. completion metadata includes prompt_tokens, completion_tokens,
//      stop_reason, duration_ms
//
// Failures here mean the wire contract drifted and the matrix test
// will fail too — that's the early-warning purpose of having both.
//
// Gated on RUN_ANTHROPIC_REAL_E2E=1 + ANTHROPIC_API_KEY.
func TestAnthropicRealInspectorContract(t *testing.T) {
	adapter, model := newRealAnthropicAdapter(t)

	rec := &llmtypes.InspectorRecorder{}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with OK."}}},
		},
		llmtypes.WithMaxTokens(16),
		llmtypes.WithInspectorSink(rec),
	)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("no choices in response")
	}

	events := rec.Events()
	assertInspectorContract(t, events, "anthropic", model)
}

// assertInspectorContract is the shared assertion used by every
// per-adapter inspector test and the cross-adapter matrix test.
// Centralising the assertion is what keeps the unified-interface
// contract stable as we add providers — each adapter implements the
// emission, this helper enforces the shape.
func assertInspectorContract(t *testing.T, events []llmtypes.InspectorEvent, wantProvider, wantModel string) {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("no inspector events emitted; adapter is not wired up")
	}

	// Phase ordering: request first, completion last, no error.
	if events[0].Phase != llmtypes.InspectorPhaseRequest {
		t.Fatalf("first event phase = %q, want %q", events[0].Phase, llmtypes.InspectorPhaseRequest)
	}
	last := events[len(events)-1]
	if last.Phase != llmtypes.InspectorPhaseCompletion {
		t.Fatalf("last event phase = %q, want %q (events: %v)", last.Phase, llmtypes.InspectorPhaseCompletion, phaseSequence(events))
	}
	for _, ev := range events {
		if ev.Phase == llmtypes.InspectorPhaseError {
			t.Fatalf("unexpected error event: %+v", ev)
		}
	}

	// At least one stream event (every modern provider emits chunks).
	sawEvent := false
	for _, ev := range events {
		if ev.Phase == llmtypes.InspectorPhaseEvent {
			sawEvent = true
			break
		}
	}
	if !sawEvent {
		t.Fatalf("no event-phase entries; adapter must emit at least one mid-stream event. Phases: %v", phaseSequence(events))
	}

	// Provider + Model are set on every event.
	for i, ev := range events {
		if ev.Provider != wantProvider {
			t.Fatalf("events[%d].Provider = %q, want %q", i, ev.Provider, wantProvider)
		}
		if ev.Model != wantModel {
			t.Fatalf("events[%d].Model = %q, want %q", i, ev.Model, wantModel)
		}
		if ev.Timestamp.IsZero() {
			t.Fatalf("events[%d].Timestamp is zero", i)
		}
	}

	// Seq monotonic without gaps, starting at 1.
	for i, ev := range events {
		if ev.Seq != i+1 {
			t.Fatalf("events[%d].Seq = %d, want %d (Seq must be 1-based monotonic)", i, ev.Seq, i+1)
		}
	}

	// Completion metadata contract.
	req := events[0]
	if req.Metadata["message_count"] == nil {
		t.Fatal("request event missing required key 'message_count'")
	}
	if last.Metadata["prompt_tokens"] == nil {
		t.Fatal("completion event missing required key 'prompt_tokens'")
	}
	if last.Metadata["completion_tokens"] == nil {
		t.Fatal("completion event missing required key 'completion_tokens'")
	}
	if last.Metadata["stop_reason"] == nil {
		t.Fatal("completion event missing required key 'stop_reason'")
	}
	if last.Metadata["duration_ms"] == nil {
		t.Fatal("completion event missing required key 'duration_ms'")
	}

	t.Logf("✅ %s inspector contract: %d events (%v)", wantProvider, len(events), phaseSequence(events))
}

func phaseSequence(events []llmtypes.InspectorEvent) []llmtypes.InspectorPhase {
	out := make([]llmtypes.InspectorPhase, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Phase)
	}
	return out
}
