package mcpagent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
)

// Replays Excellence 2026-09-29 06:27: live input sent into a running muse
// Session.Run was accepted by the CLI as the Run's response completed. Run
// returned, nothing read the CLI, and its reply to the input never reached the
// host. The Run's exit must hand the input to a follow-up watch.
func newFollowupTestSession(t *testing.T, final func() string) (*Session, *retainedCompletionCapture, func()) {
	t.Helper()
	capture := &retainedCompletionCapture{ready: make(chan struct{}, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		agent:                    &Agent{sessionID: t.Name(), provider: llm.ProviderMuseCLI, listeners: []AgentEventListener{capture}},
		watchCtx:                 ctx,
		watchCancel:              cancel,
		retainedFinalResponse:    func(llm.Provider, string, time.Time) string { return final() },
		retainedProgressMessages: (&transcriptFixture{}).read,
	}
	return s, capture, cancel
}

func shortFollowupGrace(t *testing.T) {
	previous := retainedLiveInputFinalGrace
	retainedLiveInputFinalGrace = 200 * time.Millisecond
	t.Cleanup(func() { retainedLiveInputFinalGrace = previous })
}

func waitCompletion(t *testing.T, capture *retainedCompletionCapture) *events.UnifiedCompletionEvent {
	t.Helper()
	select {
	case <-capture.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("no follow-up completion")
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.events[len(capture.events)-1].Data.(*events.UnifiedCompletionEvent)
}

func TestRunExitHandsLiveInputToFollowupWatch(t *testing.T) {
	shortFollowupGrace(t)
	var mu sync.Mutex
	final := "Supabase MCP works." // the Run's own response, still the newest final
	s, capture, cancel := newFollowupTestSession(t, func() string { mu.Lock(); defer mu.Unlock(); return final })
	defer cancel()

	lifecycle := newCanonicalTurnLifecycle("")
	inputs := &runLiveInputState{lifecycle: lifecycle}
	inputs.record("which database tables do we have", time.Now(), llm.ProviderMuseCLI, llm.CodingAgentTransportTmux)
	s.stateMu.Lock()
	inputs.ended, inputs.runText = true, "Supabase MCP works."
	watch := s.takeRunLiveInputFollowupLocked(inputs)
	again := s.takeRunLiveInputFollowupLocked(inputs)
	s.stateMu.Unlock()
	if watch == nil || again != nil {
		t.Fatalf("hand-off = %v / %v, want exactly one follow-up", watch, again)
	}
	s.runRetainedCompletionWatch(*watch)

	// The CLI picks the queued input up within the final grace (Claude appends
	// it ~100ms after end_turn): no final while it works, then its reply.
	time.Sleep(120 * time.Millisecond)
	mu.Lock()
	final = ""
	mu.Unlock()
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	final = "The project is INACTIVE (paused); no tables can be listed."
	mu.Unlock()

	completion := waitCompletion(t, capture)
	if completion.FinalResult != "The project is INACTIVE (paused); no tables can be listed." {
		t.Fatalf("follow-up final = %q", completion.FinalResult)
	}
	if completion.Metadata["source"] != "mcpagent_session" || completion.Metadata[AnsweredByPreviousResponseMetadataKey] != nil {
		t.Fatalf("follow-up metadata = %#v", completion.Metadata)
	}
}

func TestRunFollowupAnsweredBySteeredRunEmitsNoDuplicateText(t *testing.T) {
	shortFollowupGrace(t)
	s, capture, cancel := newFollowupTestSession(t, func() string { return "Tables: users, orders." })
	defer cancel()
	inputs := &runLiveInputState{lifecycle: newCanonicalTurnLifecycle("")}
	inputs.record("also list tables", time.Now(), llm.ProviderMuseCLI, llm.CodingAgentTransportTmux)
	s.stateMu.Lock()
	inputs.ended, inputs.runText = true, "MCP works.\n\nTables:  users, orders."
	watch := s.takeRunLiveInputFollowupLocked(inputs)
	s.stateMu.Unlock()
	s.runRetainedCompletionWatch(*watch)

	completion := waitCompletion(t, capture)
	if completion.FinalResult != "" || completion.Metadata[AnsweredByPreviousResponseMetadataKey] != true {
		t.Fatalf("answered completion = final %q metadata %#v", completion.FinalResult, completion.Metadata)
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.retainedActive || len(s.history) != 0 {
		t.Fatalf("answered follow-up left active=%v history=%d", s.retainedActive, len(s.history))
	}
}

// The muse durable ack can land after the Run returned. The Run's exit sees a
// delivery in flight and defers; the Send that finishes it makes the hand-off.
func TestLateDeliveryAfterRunExitStillHandsOff(t *testing.T) {
	s, _, cancel := newFollowupTestSession(t, func() string { return "" })
	defer cancel()
	inputs := &runLiveInputState{lifecycle: newCanonicalTurnLifecycle(""), inflight: 1}
	s.stateMu.Lock()
	inputs.ended = true
	if w := s.takeRunLiveInputFollowupLocked(inputs); w != nil {
		t.Fatal("hand-off while a delivery is still in flight")
	}
	inputs.inflight--
	inputs.record("late", time.Now(), llm.ProviderMuseCLI, llm.CodingAgentTransportTmux)
	w := s.takeRunLiveInputFollowupLocked(inputs)
	s.stateMu.Unlock()
	if w == nil || !w.followup || w.input != "late" {
		t.Fatalf("late hand-off = %#v", w)
	}
}

func TestRunCompletionCarriesLiveInputFollowupMarker(t *testing.T) {
	lifecycle := newCanonicalTurnLifecycle("")
	lifecycle.markLiveInputFollowup()
	completion := events.NewUnifiedCompletionEvent("session", "simple", "q", "a", "completed", time.Second, 1)
	if !lifecycle.prepareEvent(completion) || completion.Metadata[LiveInputFollowupMetadataKey] != true {
		t.Fatalf("metadata = %#v", completion.Metadata)
	}
}
