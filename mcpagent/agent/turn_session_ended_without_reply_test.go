package mcpagent

import (
	"context"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/llm"
)

// A turn typed into a running terminal that the CLI's own record shows ended with no reply (the person pressed
// Escape) has no final text to find; the watch must still settle it, as interrupted, so the host ends the turn
// (2026-10-15: a Pi turn aborted by Escape left the chat Working).
func TestRetainedWatchSettlesATurnThatEndedWithoutAReply(t *testing.T) {
	previous := retainedEndedWithoutReplyGrace
	retainedEndedWithoutReplyGrace = 50 * time.Millisecond
	defer func() { retainedEndedWithoutReplyGrace = previous }()

	capture := &retainedCompletionCapture{ready: make(chan struct{}, 2)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &Session{
		agent:                 &Agent{sessionID: "aborted-pi-turn", provider: llm.ProviderPiCLI, listeners: []AgentEventListener{capture}},
		watchCtx:              ctx,
		retainedFinalResponse: func(llm.Provider, string, time.Time) string { return "" },
		retainedEndedWithoutReply: func(llm.Provider, string, time.Time) (string, bool) {
			return "error", true
		},
	}
	session.startRetainedCompletionWatch(newCanonicalTurnLifecycle(""), "run the long command", llm.ProviderPiCLI, llm.CodingAgentTransportTmux)

	select {
	case <-capture.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("a turn that ended without a reply never settled")
	}
	session.stateMu.Lock()
	active := session.retainedActive
	session.stateMu.Unlock()
	if active {
		t.Fatal("the retained turn is still marked active after it settled")
	}
}
