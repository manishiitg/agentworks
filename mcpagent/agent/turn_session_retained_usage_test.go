package mcpagent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type usageEventCapture struct {
	mu     sync.Mutex
	ends   []*events.LLMGenerationEndEvent
	doneCh chan struct{}
}

func (c *usageEventCapture) Name() string { return "usage-event-capture" }

func (c *usageEventCapture) HandleEvent(_ context.Context, event *events.AgentEvent) error {
	if event == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch data := event.Data.(type) {
	case *events.LLMGenerationEndEvent:
		c.ends = append(c.ends, data)
	case *events.UnifiedCompletionEvent:
		select {
		case c.doneCh <- struct{}{}:
		default:
		}
	}
	return nil
}

// A turn typed into the running terminal reports the usage the CLI recorded for it: one generation-end event per
// reported run, each with its own tokens, before the completion closes the turn.
func TestRetainedTurnReportsTheUsageTheCLIRecordedBeforeItCompletes(t *testing.T) {
	capture := &usageEventCapture{doneCh: make(chan struct{}, 1)}
	agent := &Agent{logger: loggerv2.NewNoop(), sessionID: "retained-usage-session", provider: llm.ProviderMuseCLI, modelID: "muse-spark-1.3-contributor", listeners: []AgentEventListener{capture}}
	watchCtx, watchCancel := context.WithCancel(context.Background())
	defer watchCancel()
	session := &Session{
		agent:                 agent,
		watchCtx:              watchCtx,
		watchCancel:           watchCancel,
		retainedFinalResponse: func(llm.Provider, string, time.Time) string { return "done" },
		retainedUsage: func(provider llm.Provider, owner, model string, _ time.Time) []llmtypes.RetainedTurnUsage {
			if provider != llm.ProviderMuseCLI || owner != "retained-usage-session" || model != "muse-spark-1.3-contributor" {
				t.Fatalf("usage reader identity = %s/%s/%s", provider, owner, model)
			}
			return []llmtypes.RetainedTurnUsage{
				{ID: "run-1", Usage: llmtypes.Usage{InputTokens: 200, OutputTokens: 20, TotalTokens: 220}},
				{ID: "run-2", Usage: llmtypes.Usage{InputTokens: 300, OutputTokens: 30, TotalTokens: 330}},
			}
		},
	}
	session.startRetainedCompletionWatch(newCanonicalTurnLifecycle(""), "follow up", llm.ProviderMuseCLI, llm.CodingAgentTransportTmux)
	select {
	case <-capture.doneCh:
	case <-time.After(3 * time.Second):
		t.Fatal("retained turn did not complete")
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if len(capture.ends) != 2 || capture.ends[0].UsageMetrics.PromptTokens != 200 || capture.ends[1].UsageMetrics.CompletionTokens != 30 {
		t.Fatalf("expected the two reported runs as generation-end events, got %+v", capture.ends)
	}
	if capture.ends[0].Metadata["usage_source"] != "retained" || capture.ends[0].Metadata["usage_source_id"] != "run-1" || capture.ends[1].Metadata["usage_source_id"] != "run-2" {
		t.Fatalf("each event must name the CLI run it came from, got %v / %v", capture.ends[0].Metadata, capture.ends[1].Metadata)
	}
}
