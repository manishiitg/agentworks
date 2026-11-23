package mcpagent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

type orderedEndCapture struct {
	mu    sync.Mutex
	order []string
	end   *events.StreamingEndEvent
	done  chan struct{}
}

func (c *orderedEndCapture) Name() string { return "ordered-end-capture" }

func (c *orderedEndCapture) HandleEvent(_ context.Context, event *events.AgentEvent) error {
	if event == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch data := event.Data.(type) {
	case *events.UnifiedCompletionEvent:
		c.order = append(c.order, "completion")
	case *events.StreamingEndEvent:
		c.order = append(c.order, "stream_end")
		c.end = data
		select {
		case c.done <- struct{}{}:
		default:
		}
	}
	return nil
}

// A turn typed into the running terminal ends its stream when it completes: one terminal streaming_end follows the
// completion (so a handler that settles on the first terminal event is not confused), stamped no later than it.
func TestRetainedTurnEndsItsStreamAfterTheCompletion(t *testing.T) {
	capture := &orderedEndCapture{done: make(chan struct{}, 1)}
	agent := &Agent{logger: loggerv2.NewNoop(), sessionID: "retained-end-session", provider: llm.ProviderPiCLI, listeners: []AgentEventListener{capture}}
	watchCtx, watchCancel := context.WithCancel(context.Background())
	defer watchCancel()
	session := &Session{
		agent:                 agent,
		watchCtx:              watchCtx,
		watchCancel:           watchCancel,
		retainedFinalResponse: func(llm.Provider, string, time.Time) string { return "done" },
	}
	before := time.Now()
	session.startRetainedCompletionWatch(newCanonicalTurnLifecycle(""), "follow up", llm.ProviderPiCLI, llm.CodingAgentTransportTmux)
	select {
	case <-capture.done:
	case <-time.After(3 * time.Second):
		t.Fatal("the retained turn never ended its stream")
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if len(capture.order) != 2 || capture.order[0] != "completion" || capture.order[1] != "stream_end" {
		t.Fatalf("want the completion then one stream end, got %v", capture.order)
	}
	if capture.end.Metadata["kind"] != "terminal" || capture.end.Metadata["provider"] != string(llm.ProviderPiCLI) || capture.end.Timestamp.Before(before) {
		t.Fatalf("stream end = %+v", capture.end)
	}
}
