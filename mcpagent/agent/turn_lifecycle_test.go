package mcpagent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
)

type canonicalTurnCapture struct {
	mu     sync.Mutex
	events []*events.AgentEvent
}

func (c *canonicalTurnCapture) Name() string { return "canonical-turn-capture" }

func (c *canonicalTurnCapture) HandleEvent(_ context.Context, event *events.AgentEvent) error {
	c.mu.Lock()
	c.events = append(c.events, event)
	c.mu.Unlock()
	return nil
}

func (c *canonicalTurnCapture) snapshot() []*events.AgentEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*events.AgentEvent(nil), c.events...)
}

func TestCanonicalTurnLifecycleSuppressesDuplicateCompletion(t *testing.T) {
	capture := &canonicalTurnCapture{}
	agent := &Agent{sessionID: "canonical-duplicate", listeners: []AgentEventListener{capture}}
	lifecycle := newCanonicalTurnLifecycle("turn_test_duplicate")
	ctx := withCanonicalTurnLifecycle(context.Background(), lifecycle)

	first := events.NewUnifiedCompletionEvent("simple", "chat", "hello", "done", "completed", time.Second, 1)
	second := events.NewUnifiedCompletionEvent("simple", "chat", "hello", "duplicate", "completed", 2*time.Second, 1)
	agent.emitTypedEvent(ctx, first)
	agent.emitTypedEvent(ctx, second)

	got := capture.snapshot()
	if len(got) != 1 {
		t.Fatalf("terminal events = %d, want exactly one", len(got))
	}
	completion, ok := got[0].Data.(*events.UnifiedCompletionEvent)
	if !ok || completion.FinalResult != "done" {
		t.Fatalf("completion = %#v", got[0].Data)
	}
	if got[0].TurnID != lifecycle.id || completion.Metadata["turn_id"] != lifecycle.id {
		t.Fatalf("turn identity = wrapper:%q metadata:%v want %q", got[0].TurnID, completion.Metadata["turn_id"], lifecycle.id)
	}
}

// TestP0CanonicalTurnContract is the credential-free mandatory release gate
// for IC-12. It exercises the public Session.Run boundary: every accepted turn
// has one stable identity and exactly one terminal completion, including an
// error before any provider is called.
func TestP0CanonicalTurnContract(t *testing.T) {
	capture := &canonicalTurnCapture{}
	agent := &Agent{sessionID: "canonical-run-error", listeners: []AgentEventListener{capture}}
	session := &Session{agent: agent}

	result, err := session.Run(context.Background(), Turn{
		ID:         "turn_requested_id",
		Input:      "hello",
		ToolPolicy: ToolPolicy{AllowedTools: []string{" invalid "}},
	})
	if err == nil {
		t.Fatal("expected invalid tool policy")
	}
	if result.TurnID != "turn_requested_id" {
		t.Fatalf("result turn id = %q", result.TurnID)
	}
	got := capture.snapshot()
	if len(got) != 1 {
		t.Fatalf("events = %d, want one canonical error completion", len(got))
	}
	completion, ok := got[0].Data.(*events.UnifiedCompletionEvent)
	if !ok || completion.Status != "error" || completion.Error == "" {
		t.Fatalf("completion = %#v", got[0].Data)
	}
	if got[0].TurnID != result.TurnID || completion.Metadata["canonical_turn_completion"] != true {
		t.Fatalf("canonical completion identity = wrapper:%q metadata:%v", got[0].TurnID, completion.Metadata)
	}
}

func TestCanonicalTurnLifecycleIsolatedAcrossConcurrentSessions(t *testing.T) {
	const workers = 2
	turnIDs := make(chan string, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			capture := &canonicalTurnCapture{}
			agent := &Agent{listeners: []AgentEventListener{capture}}
			session := &Session{agent: agent}
			result, err := session.Run(context.Background(), Turn{
				Input:      "hello",
				ToolPolicy: ToolPolicy{AllowedTools: []string{" invalid "}},
			})
			turnIDs <- result.TurnID
			errs <- err
			got := capture.snapshot()
			if len(got) != 1 || got[0].TurnID != result.TurnID {
				t.Errorf("completion identity mismatch: events=%d turn=%q", len(got), result.TurnID)
			}
		}()
	}
	wg.Wait()
	close(turnIDs)
	close(errs)
	for err := range errs {
		if err == nil {
			t.Fatal("expected invalid policy error")
		}
	}
	seen := map[string]bool{}
	for id := range turnIDs {
		if id == "" || seen[id] {
			t.Fatalf("turn id is empty or duplicated: %q", id)
		}
		seen[id] = true
	}
}
