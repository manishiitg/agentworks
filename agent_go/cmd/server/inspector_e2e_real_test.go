package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/gorilla/mux"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	anthropicadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/anthropic"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/inspector"
)

// TestInspectorHTTPCapturesRealAnthropicEvents is the cross-stack
// inspector e2e: real Anthropic call with a scoped sink wired up,
// events flow into the InspectorStore, HTTP GET /api/inspector/<sess>
// returns the timeline, every event carries the StepContext attached
// at the scope.
//
// The test exercises the complete chain the inspector panel relies
// on in production:
//
//	real provider streaming
//	  → anthropic adapter emits InspectorEvents at every phase
//	  → ScopedInspectorSink injects StepContext (step_id, phase, etc.)
//	  → inspector.Store.Sink() forwards into the per-session ring
//	  → handleInspectorEvents reads the ring and serves JSON
//
// What this catches:
//
//   - The InspectorSink option actually plumbs through the adapter
//     stack (regression for llmtypes.WithInspectorSink wiring)
//   - The store correctly buckets events by SessionID (from the
//     scoped sink) rather than dropping them
//   - The HTTP handler returns the events with the right shape and
//     respects the `since` cursor
//   - StepContext fields survive the round-trip
//
// Gated on RUN_ANTHROPIC_REAL_E2E=1 + ANTHROPIC_API_KEY.
func TestInspectorHTTPCapturesRealAnthropicEvents(t *testing.T) {
	if os.Getenv("RUN_ANTHROPIC_REAL_E2E") == "" {
		t.Skip("set RUN_ANTHROPIC_REAL_E2E=1 to run this inspector HTTP e2e")
	}
	apiKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY required")
	}
	model := strings.TrimSpace(os.Getenv("ANTHROPIC_REAL_E2E_MODEL"))
	if model == "" {
		model = "claude-haiku-4-5"
	}

	// Spin up an isolated API with an inspector store.
	store := inspector.NewStore()
	api := &StreamingAPI{inspectorStore: store}

	const sessionID = "test-session-inspector-001"

	// Build the scoped sink the way the chat handler would for an
	// inspector-enabled session. Step context gets attached by the
	// scope; the adapter only knows about the inner emitter.
	stepCtx := llmtypes.StepContext{
		SessionID:     sessionID,
		StepID:        "fetch-customer-data",
		StepType:      "scripted",
		Phase:         "execution",
		StepName:      "Fetch customer data",
		StepIndex:     1,
		StepTotal:     2,
		StepStartedAt: time.Now().UTC(),
		AgentName:     "worker",
		Attempt:       1,
		CallPurpose:   "main_generation",
		WorkflowName:  "test-inspector-workflow",
	}
	sink := llmtypes.NewScopedInspectorSink(store.Sink(), stepCtx)

	// Real Anthropic call with the scoped sink attached.
	client := anthropic.NewClient(anthropicoption.WithAPIKey(apiKey))
	adapter := anthropicadapter.NewAnthropicAdapter(client, model, &e2eMockLogger{})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply with OK."}}},
		},
		llmtypes.WithMaxTokens(16),
		llmtypes.WithInspectorSink(sink),
	)
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}

	// HTTP roundtrip: /api/inspector/<session_id>
	req := httptest.NewRequest(http.MethodGet, "/api/inspector/"+sessionID, nil)
	req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
	rec := httptest.NewRecorder()
	api.handleInspectorEvents(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/api/inspector/<sess> status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var summary inspectorSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("decode summary: %v\nbody=%s", err, rec.Body.String())
	}

	if summary.SessionID != sessionID {
		t.Fatalf("summary.SessionID = %q, want %q", summary.SessionID, sessionID)
	}
	if summary.Count == 0 {
		t.Fatal("no events in summary; adapter→sink→store chain not working")
	}
	if summary.LatestSeq == 0 {
		t.Fatal("LatestSeq = 0; store didn't assign sequence numbers")
	}

	// Phase ordering: request first, completion last (no errors).
	first := summary.Events[0].Event
	last := summary.Events[len(summary.Events)-1].Event
	if first.Phase != llmtypes.InspectorPhaseRequest {
		t.Fatalf("first phase = %q, want request", first.Phase)
	}
	if last.Phase != llmtypes.InspectorPhaseCompletion {
		t.Fatalf("last phase = %q, want completion", last.Phase)
	}

	// Step attribution: every event must carry the scope's StepID.
	for i, se := range summary.Events {
		if se.Event.StepContext.SessionID != sessionID {
			t.Fatalf("events[%d].StepContext.SessionID = %q, want %q", i, se.Event.StepContext.SessionID, sessionID)
		}
		if se.Event.StepContext.StepID != "fetch-customer-data" {
			t.Fatalf("events[%d].StepContext.StepID = %q, want fetch-customer-data", i, se.Event.StepContext.StepID)
		}
		if se.Event.StepContext.WorkflowName != "test-inspector-workflow" {
			t.Fatalf("events[%d].StepContext.WorkflowName lost", i)
		}
	}

	// `since` cursor test: a follow-up GET with since=LatestSeq must
	// return zero events (we made no further calls).
	req2 := httptest.NewRequest(http.MethodGet, "/api/inspector/"+sessionID+"?since=999999", nil)
	req2 = mux.SetURLVars(req2, map[string]string{"session_id": sessionID})
	rec2 := httptest.NewRecorder()
	api.handleInspectorEvents(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("since-cursor status = %d", rec2.Code)
	}
	var summary2 inspectorSummary
	_ = json.Unmarshal(rec2.Body.Bytes(), &summary2)
	if summary2.Count != 0 {
		t.Fatalf("since=999999 returned %d events, want 0", summary2.Count)
	}

	t.Logf("✅ inspector HTTP e2e: %d events captured for session %q (latest_seq=%d, step=%q)",
		summary.Count, sessionID, summary.LatestSeq, first.StepContext.StepID)
}

// TestInspectorHTTPUnknownSessionReturns404 locks in the
// not-found contract: requesting a session the inspector never saw
// returns 404, not an empty 200.
func TestInspectorHTTPUnknownSessionReturns404(t *testing.T) {
	api := &StreamingAPI{inspectorStore: inspector.NewStore()}
	req := httptest.NewRequest(http.MethodGet, "/api/inspector/no-such-session", nil)
	req = mux.SetURLVars(req, map[string]string{"session_id": "no-such-session"})
	rec := httptest.NewRecorder()
	api.handleInspectorEvents(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown session status = %d, want 404", rec.Code)
	}
}

// TestInspectorHTTPMissingStoreReturns503 verifies the handler's
// graceful behavior when the inspector store is nil (e.g. during
// startup or in a stripped-down test harness).
func TestInspectorHTTPMissingStoreReturns503(t *testing.T) {
	api := &StreamingAPI{} // no inspectorStore
	req := httptest.NewRequest(http.MethodGet, "/api/inspector/x", nil)
	req = mux.SetURLVars(req, map[string]string{"session_id": "x"})
	rec := httptest.NewRecorder()
	api.handleInspectorEvents(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil-store status = %d, want 503", rec.Code)
	}
}
