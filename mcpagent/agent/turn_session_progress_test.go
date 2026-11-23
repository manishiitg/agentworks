package mcpagent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type retainedProgressCapture struct{ events chan *events.AgentEvent }

func (c *retainedProgressCapture) Name() string { return "retained-progress" }
func (c *retainedProgressCapture) HandleEvent(_ context.Context, event *events.AgentEvent) error {
	c.events <- event
	return nil
}

func TestRetainedProgressStreamsWhileFinalIsPending(t *testing.T) {
	for _, provider := range []llm.Provider{llm.ProviderClaudeCode, llm.ProviderCodexCLI, llm.ProviderCursorCLI, llm.ProviderMuseCLI, llm.ProviderPiCLI, llm.ProviderAgyCLI} {
		t.Run(string(provider), func(t *testing.T) { testRetainedProgressStreamsWhileFinalIsPending(t, provider) })
	}
}

func testRetainedProgressStreamsWhileFinalIsPending(t *testing.T, provider llm.Provider) {
	capture := &retainedProgressCapture{events: make(chan *events.AgentEvent, 20)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var reads atomic.Int32
	s := &Session{
		agent:                 &Agent{sessionID: t.Name(), listeners: []AgentEventListener{capture}},
		watchCtx:              ctx,
		retainedFinalResponse: func(llm.Provider, string, time.Time) string { return "" },
		retainedProgressMessages: func(llm.Provider, string) []llmtypes.MessageContent {
			if reads.Add(1) > 1 {
				return nil
			}
			return []llmtypes.MessageContent{
				llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "test login"),
				{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{
					llmtypes.TextContent{Text: "Checking login now."},
					llmtypes.ToolCall{ID: "browser-check"},
				}},
			}
		},
	}
	lifecycle := newCanonicalTurnLifecycle("")
	s.startRetainedCompletionWatch(lifecycle, "test login", provider, llm.CodingAgentTransportTmux)
	select {
	case e := <-capture.events:
		chunk, ok := e.Data.(*events.StreamingChunkEvent)
		if !ok || chunk.Content != "Checking login now." || chunk.Source != "transcript" || chunk.IsDelta || e.TurnID != lifecycle.id {
			t.Fatalf("wrong progress event: %#v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("progress was hidden until final completion")
	}
	select {
	case e := <-capture.events:
		t.Fatalf("repeated poll duplicated progress or completed early: %#v", e)
	case <-time.After(600 * time.Millisecond):
	}
	s.stateMu.Lock()
	active := s.retainedActive
	s.stateMu.Unlock()
	if !active {
		t.Fatal("progress settled the turn")
	}
	cancel()
	// A replaced/closed watcher must not publish a late database read.
	s.stateMu.Lock()
	s.closed = true
	s.stateMu.Unlock()
	index := 0
	s.emitRetainedProgress(lifecycle, 1, provider, func(llm.Provider, string) []llmtypes.MessageContent {
		t.Error("stale watcher consumed progress")
		return nil
	}, &index)
	select {
	case e := <-capture.events:
		t.Fatalf("stale progress: %#v", e)
	default:
	}
}

func TestRetainedCompletionFlushesProgressCommittedAfterPoll(t *testing.T) {
	capture := &retainedProgressCapture{events: make(chan *events.AgentEvent, 20)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var finalRead atomic.Bool
	var delivered atomic.Bool
	s := &Session{
		agent: &Agent{sessionID: t.Name(), listeners: []AgentEventListener{capture}}, watchCtx: ctx,
		retainedProgressMessages: func(llm.Provider, string) []llmtypes.MessageContent {
			if !finalRead.Load() || delivered.Swap(true) {
				return nil
			}
			return []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "Updating the dashboard with a clear What we test tab.")}
		},
		retainedFinalResponse: func(llm.Provider, string, time.Time) string { finalRead.Store(true); return "Dashboard updated." },
	}
	s.startRetainedCompletionWatch(newCanonicalTurnLifecycle(""), "update dashboard", llm.ProviderCursorCLI, llm.CodingAgentTransportTmux)
	for i := 0; i < 2; i++ {
		select {
		case event := <-capture.events:
			if i == 0 {
				if _, ok := event.Data.(*events.StreamingChunkEvent); !ok {
					t.Fatalf("completion preceded progress: %T", event.Data)
				}
			} else {
				if _, ok := event.Data.(*events.UnifiedCompletionEvent); !ok {
					t.Fatalf("expected completion: %T", event.Data)
				}
			}
		case <-time.After(2 * time.Second):
			t.Fatal("missing progress or completion")
		}
	}
}

// transcriptFixture replays Claude transcript commits through an incremental
// cursor, like the provider's retained progress reader.
type transcriptFixture struct {
	mu     sync.Mutex
	rows   []llmtypes.MessageContent
	cursor int
}

func (f *transcriptFixture) commit(rows ...llmtypes.MessageContent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, rows...)
}

func (f *transcriptFixture) read(llm.Provider, string) []llmtypes.MessageContent {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := append([]llmtypes.MessageContent(nil), f.rows[f.cursor:]...)
	f.cursor = len(f.rows)
	return rows
}

func nextProgressText(t *testing.T, capture *retainedProgressCapture, within time.Duration) string {
	t.Helper()
	select {
	case e := <-capture.events:
		chunk, ok := e.Data.(*events.StreamingChunkEvent)
		if !ok || chunk.Source != "transcript" || chunk.IsDelta {
			t.Fatalf("expected a whole transcript message, got %#v", e.Data)
		}
		return chunk.Content
	case <-time.After(within):
		t.Fatal("assistant text was not emitted")
	}
	return ""
}

func assertNoProgress(t *testing.T, capture *retainedProgressCapture, within time.Duration) {
	t.Helper()
	select {
	case e := <-capture.events:
		t.Fatalf("unexpected event (duplicate or early completion): %#v", e.Data)
	case <-time.After(within):
	}
}

// Replays the RTS SDE crew turn: text, a tool_use that ran for two minutes,
// its tool_result, then more text. A steer sent meanwhile keeps Send inside
// delivery (Claude queues it until the tool returns); the narration written
// before the tool must still reach the chat while the tool runs.
func TestRetainedProgressIsNotHeldBehindPendingSteer(t *testing.T) {
	capture := &retainedProgressCapture{events: make(chan *events.AgentEvent, 20)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture := &transcriptFixture{}
	s := &Session{
		agent:                    &Agent{sessionID: t.Name(), listeners: []AgentEventListener{capture}},
		watchCtx:                 ctx,
		retainedFinalResponse:    func(llm.Provider, string, time.Time) string { return "" },
		retainedProgressMessages: fixture.read,
	}
	s.startRetainedCompletionWatch(newCanonicalTurnLifecycle(""), "redeploy", llm.ProviderClaudeCode, llm.CodingAgentTransportTmux)

	// A steer is being delivered for the whole tool call.
	s.sendMu.Lock()
	fixture.commit(
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "Re-triggered the failed deploy; watching it now."),
		llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.ToolCall{ID: "deploy-watch"}}},
	)
	if got := nextProgressText(t, capture, 2*time.Second); got != "Re-triggered the failed deploy; watching it now." {
		t.Fatalf("first text = %q", got)
	}
	assertNoProgress(t, capture, 600*time.Millisecond)

	// Tool returns; Claude takes the queued steer and keeps narrating.
	fixture.commit(
		llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeTool, Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: "deploy-watch", Content: "ok"}}},
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "Deploy is green."),
	)
	s.sendMu.Unlock()
	if got := nextProgressText(t, capture, 2*time.Second); got != "Deploy is green." {
		t.Fatalf("second text = %q", got)
	}

	// One message with two text blocks, then a message cut off by an interrupt:
	// every block once, in order, and no completion is inferred.
	fixture.commit(llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{
		llmtypes.TextContent{Text: "Summary first."},
		llmtypes.TextContent{Text: "Then the details."},
	}})
	if got := nextProgressText(t, capture, 2*time.Second); got != "Summary first." {
		t.Fatalf("first block = %q", got)
	}
	if got := nextProgressText(t, capture, time.Second); got != "Then the details." {
		t.Fatalf("second block = %q", got)
	}
	fixture.commit(llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "Starting the rollback"))
	if got := nextProgressText(t, capture, 2*time.Second); got != "Starting the rollback" {
		t.Fatalf("interrupted text = %q", got)
	}
	assertNoProgress(t, capture, 600*time.Millisecond)
	s.stateMu.Lock()
	active := s.retainedActive
	s.stateMu.Unlock()
	if !active {
		t.Fatal("progress settled the turn")
	}
}

// Thinking committed during a follow-up turn is sent as thinking, never as a
// reply chunk, in both value and pointer form.
func TestRetainedProgressSendsThinkingAsThinking(t *testing.T) {
	capture := &retainedProgressCapture{events: make(chan *events.AgentEvent, 20)}
	s := &Session{agent: &Agent{sessionID: t.Name(), listeners: []AgentEventListener{capture}}, retainedActive: true, retainedSeq: 1}
	lifecycle := newCanonicalTurnLifecycle("")
	index := 0
	s.emitRetainedProgress(lifecycle, 1, llm.ProviderPiCLI, func(llm.Provider, string) []llmtypes.MessageContent {
		return []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{
			llmtypes.ThinkingContent{Thinking: "Identifying session clues."},
			&llmtypes.ThinkingContent{Thinking: "Checking the report."},
			llmtypes.TextContent{Text: "Checking the report now."},
		}}}
	}, &index)
	var thinking []string
	var replies []string
	for len(thinking)+len(replies) < 3 {
		select {
		case e := <-capture.events:
			switch data := e.Data.(type) {
			case *events.ConversationThinkingEvent:
				thinking = append(thinking, data.Thinking)
			case *events.StreamingChunkEvent:
				replies = append(replies, data.Content)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("missing events: thinking=%q replies=%q", thinking, replies)
		}
	}
	if len(thinking) != 2 || thinking[0] != "Identifying session clues." || thinking[1] != "Checking the report." {
		t.Fatalf("thinking = %q", thinking)
	}
	if len(replies) != 1 || replies[0] != "Checking the report now." {
		t.Fatalf("thinking leaked into reply chunks: %q", replies)
	}
}

// PLAT-746-984: Session.Send on a retained CLI must publish its native todo list too.
func TestRetainedProgressPublishesNativeChecklistAfterSuccessfulTool(t *testing.T) {
	capture := &retainedProgressCapture{events: make(chan *events.AgentEvent, 16)}
	s := &Session{agent: &Agent{sessionID: "retained-plan", listeners: []AgentEventListener{capture}}, retainedActive: true, retainedSeq: 1}
	lifecycle := newCanonicalTurnLifecycle("")
	tools := map[string]retainedNativeTool{}
	index := 0
	s.emitRetainedProgress(lifecycle, 1, llm.ProviderClaudeCode, func(llm.Provider, string) []llmtypes.MessageContent {
		return []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.ToolCall{ID: "todo", FunctionCall: &llmtypes.FunctionCall{Name: "TodoWrite", Arguments: `{"todos":[{"content":"Inspect","status":"completed"}]}`}}}},
		}
	}, &index, tools)
	// Completion arrives in a later native read and has no argument payload of its own.
	s.emitRetainedProgress(lifecycle, 1, llm.ProviderClaudeCode, func(llm.Provider, string) []llmtypes.MessageContent {
		return []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeTool, Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: "todo", Content: "Updated"}}},
		}
	}, &index, tools)
	var plans []*events.AgentEvent
	for len(capture.events) > 0 {
		event := <-capture.events
		if event.Type == events.CodingAgentPlan {
			plans = append(plans, event)
		}
	}
	if len(plans) != 1 || plans[0].TurnID != lifecycle.id {
		t.Fatalf("retained plan missing or wrong turn: %+v", plans)
	}
	plan := plans[0].Data.(*events.CodingAgentPlanEvent)
	if plan.Items[0].Status != "completed" || plan.Provider != string(llm.ProviderClaudeCode) || plan.SessionID != "retained-plan" {
		t.Fatalf("wrong native checklist: %+v", plan)
	}
	if !s.retainedActive {
		t.Fatal("a checklist must not settle the retained turn")
	}
}
