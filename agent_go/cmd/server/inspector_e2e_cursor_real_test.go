package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	cursorcliadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/inspector"
)

// TestInspectorHTTPCapturesRealCursorEvents is the cursor-cli
// counterpart to TestInspectorHTTPCapturesRealAnthropicEvents.
//
// Cursor CLI runs through the tmux coding-agent transport. The adapter routes
// through WithObservability so the inspector sink receives request + completion
// events the same shape the API adapters produce. This test proves the chain:
//
//	real cursor-agent run
//	  → cursor-cli adapter emits InspectorEvents at every phase
//	  → ScopedInspectorSink injects StepContext
//	  → inspector.Store.Sink() forwards into the per-session ring
//	  → handleInspectorEvents reads the ring and serves JSON
//
// Gated on RUN_CURSOR_CLI_REAL_E2E=1 + cursor-agent binary in PATH.
// (cursor-agent uses its own login state — no explicit API key
// required in the test env, just an already-authenticated install.)
func TestInspectorHTTPCapturesRealCursorEvents(t *testing.T) {
	if os.Getenv("RUN_CURSOR_CLI_REAL_E2E") == "" {
		t.Skip("set RUN_CURSOR_CLI_REAL_E2E=1 to run this inspector HTTP e2e")
	}
	if _, err := exec.LookPath("cursor-agent"); err != nil {
		t.Skipf("cursor-agent binary not found: %v", err)
	}

	model := strings.TrimSpace(os.Getenv("CURSOR_CLI_REAL_E2E_MODEL"))
	if model == "" {
		model = "cursor-cli"
	}

	store := inspector.NewStore()
	api := &StreamingAPI{inspectorStore: store}

	const sessionID = "test-session-inspector-cursor-001"

	stepCtx := llmtypes.StepContext{
		SessionID:     sessionID,
		StepID:        "cursor-cli-call",
		StepType:      "scripted",
		Phase:         "execution",
		StepName:      "Cursor CLI call",
		StepIndex:     1,
		StepTotal:     1,
		StepStartedAt: time.Now().UTC(),
		AgentName:     "worker",
		Attempt:       1,
		CallPurpose:   "main_generation",
		WorkflowName:  "test-inspector-cursor-workflow",
	}
	sink := llmtypes.NewScopedInspectorSink(store.Sink(), stepCtx)

	adapter := cursorcliadapter.NewCursorCLIAdapter("", model, &e2eMockLogger{})

	// Allow a generous budget for cold start + the run itself.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Reply with exactly the word OK and nothing else."},
			}},
		},
		llmtypes.WithInspectorSink(sink),
	)
	if err != nil {
		t.Fatalf("cursor-cli GenerateContent: %v", err)
	}

	// HTTP roundtrip.
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
		t.Fatal("no events in summary; cursor-cli adapter→sink→store chain not working")
	}
	if summary.LatestSeq == 0 {
		t.Fatal("LatestSeq = 0; store didn't assign sequence numbers")
	}

	first := summary.Events[0].Event
	last := summary.Events[len(summary.Events)-1].Event
	if first.Phase != llmtypes.InspectorPhaseRequest {
		t.Fatalf("first phase = %q, want request", first.Phase)
	}
	if last.Phase != llmtypes.InspectorPhaseCompletion {
		t.Fatalf("last phase = %q, want completion", last.Phase)
	}
	for i, se := range summary.Events {
		if se.Event.Phase == llmtypes.InspectorPhaseError {
			t.Fatalf("events[%d] unexpectedly carries an error phase: %+v", i, se.Event)
		}
		if se.Event.Provider != "cursor-cli" {
			t.Fatalf("events[%d].Provider = %q, want cursor-cli", i, se.Event.Provider)
		}
	}

	for i, se := range summary.Events {
		if se.Event.StepContext.SessionID != sessionID {
			t.Fatalf("events[%d].StepContext.SessionID = %q, want %q", i, se.Event.StepContext.SessionID, sessionID)
		}
		if se.Event.StepContext.StepID != "cursor-cli-call" {
			t.Fatalf("events[%d].StepContext.StepID = %q, want cursor-cli-call", i, se.Event.StepContext.StepID)
		}
		if se.Event.StepContext.WorkflowName != "test-inspector-cursor-workflow" {
			t.Fatalf("events[%d].StepContext.WorkflowName lost", i)
		}
	}

	t.Logf("✅ cursor-cli inspector HTTP e2e: %d events captured for session %q (latest_seq=%d)",
		summary.Count, sessionID, summary.LatestSeq)
}
