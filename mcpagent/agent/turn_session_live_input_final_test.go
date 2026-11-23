package mcpagent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
)

// Replays RTS 2026-09-24 04:19:45: follow-ups were sent into a running Claude
// response. Claude wrote that response's end_turn, then ~100ms later appended
// the queued follow-ups as user messages and kept working on them. The watcher
// refreshed for the follow-ups must not take the old end_turn as their answer.
func TestLiveInputWatcherIgnoresFinalSupersededByQueuedFollowUps(t *testing.T) {
	previous := retainedLiveInputFinalGrace
	retainedLiveInputFinalGrace = 500 * time.Millisecond
	defer func() { retainedLiveInputFinalGrace = previous }()

	var mu sync.Mutex
	final := "Found both. Here's the full picture" // old response's end_turn
	setFinal := func(v string) { mu.Lock(); final = v; mu.Unlock() }

	capture := &retainedProgressCapture{events: make(chan *events.AgentEvent, 20)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Session{
		agent:    &Agent{sessionID: t.Name(), listeners: []AgentEventListener{capture}},
		watchCtx: ctx,
		retainedFinalResponse: func(llm.Provider, string, time.Time) string {
			mu.Lock()
			defer mu.Unlock()
			return final
		},
		retainedProgressMessages: (&transcriptFixture{}).read,
	}
	s.startRetainedCompletionWatchFor(newCanonicalTurnLifecycle(""), "and if any new comments", llm.ProviderClaudeCode, llm.CodingAgentTransportTmux, true)

	// The queued follow-ups land right after the old end_turn: no final now.
	time.Sleep(150 * time.Millisecond)
	setFinal("")
	assertNoProgress(t, capture, 800*time.Millisecond)

	// The CLI finishes the follow-up's real answer.
	setFinal("Both are done: dashboard and sync command.")
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-capture.events:
			if c, ok := e.Data.(*events.UnifiedCompletionEvent); ok {
				if c.FinalResult != "Both are done: dashboard and sync command." {
					t.Fatalf("completed with %q, want the follow-up's answer", c.FinalResult)
				}
				return
			}
		case <-deadline:
			t.Fatal("follow-up never completed")
		}
	}
}

// A fresh (non-live-input) retained send keeps completing without the grace.
func TestFreshRetainedWatcherCompletesWithoutGrace(t *testing.T) {
	previous := retainedLiveInputFinalGrace
	retainedLiveInputFinalGrace = time.Hour
	defer func() { retainedLiveInputFinalGrace = previous }()
	capture := &retainedProgressCapture{events: make(chan *events.AgentEvent, 20)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Session{
		agent:                    &Agent{sessionID: t.Name(), listeners: []AgentEventListener{capture}},
		watchCtx:                 ctx,
		retainedFinalResponse:    func(llm.Provider, string, time.Time) string { return "done" },
		retainedProgressMessages: (&transcriptFixture{}).read,
	}
	s.startRetainedCompletionWatch(newCanonicalTurnLifecycle(""), "go", llm.ProviderClaudeCode, llm.CodingAgentTransportTmux)
	select {
	case e := <-capture.events:
		if _, ok := e.Data.(*events.UnifiedCompletionEvent); !ok {
			t.Fatalf("unexpected event %#v", e.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fresh watcher did not complete promptly")
	}
}
