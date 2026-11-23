package mcpagent

import (
	"context"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestObserveNativeInputWatchesWithoutDelivery(t *testing.T) {
	shortFollowupGrace(t)
	s, capture, cancel := newFollowupTestSession(t, func() string { return "Native answer." })
	defer cancel()
	s.agent.provider = llm.ProviderClaudeCode // timestamp-scoped transcript; no CLI installed/started.
	acceptedAt := time.Now().Add(-time.Second)
	id, err := s.ObserveNativeInput(context.Background(), "native prompt", acceptedAt)
	if err != nil || id == "" {
		t.Fatalf("adopt = %q, %v", id, err)
	}
	completion := waitCompletion(t, capture)
	if completion.FinalResult != "Native answer." || completion.Question != "native prompt" {
		t.Fatalf("completion = %+v", completion)
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if len(s.history) != 2 || s.retainedActive {
		t.Fatalf("history=%+v active=%v", s.history, s.retainedActive)
	}
}

func TestObserveNativeInputDuringRunUsesFollowup(t *testing.T) {
	s, _, cancel := newFollowupTestSession(t, func() string { return "" })
	defer cancel()
	s.agent.provider = llm.ProviderCodexCLI
	lifecycle := newCanonicalTurnLifecycle("")
	s.runActive, s.activeTurn = true, lifecycle
	s.runLiveInputs = &runLiveInputState{lifecycle: lifecycle}
	_, err := s.ObserveNativeInput(t.Context(), "also check tests", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if s.retainedActive || len(s.runLiveInputs.inputs) != 1 || s.runLiveInputs.inflight != 0 {
		t.Fatalf("run input handoff = %+v", s.runLiveInputs)
	}
}

func TestObserveNativeInputFailureLeavesSessionIdle(t *testing.T) {
	s, _, cancel := newFollowupTestSession(t, func() string { return "" })
	defer cancel()
	// Muse needs its own accepted sequence. A missing process must not wedge Run.
	if _, err := s.ObserveNativeInput(t.Context(), "missing native prompt", time.Now()); err == nil {
		t.Fatal("adopt succeeded without a Muse process")
	}
	if s.retainedStarting || s.retainedActive || s.activeTurn != nil {
		t.Fatal("failed adoption left an active turn")
	}
}

func TestRetainedNativeToolsPairAcrossPolls(t *testing.T) {
	capture := &retainedProgressCapture{events: make(chan *events.AgentEvent, 10)}
	s := &Session{agent: &Agent{sessionID: t.Name(), listeners: []AgentEventListener{capture}}, retainedActive: true, retainedSeq: 1}
	lifecycle := newCanonicalTurnLifecycle("")
	index := 0
	tools := map[string]retainedNativeTool{}
	for _, message := range []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.ToolCall{ID: "call1", FunctionCall: &llmtypes.FunctionCall{Name: "Read", Arguments: `{}`}}}},
		{Role: llmtypes.ChatMessageTypeTool, Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: "call1", Content: "denied", IsError: true}}},
	} {
		s.emitRetainedProgress(lifecycle, 1, llm.ProviderClaudeCode, func(llm.Provider, string) []llmtypes.MessageContent { return []llmtypes.MessageContent{message} }, &index, tools)
	}
	start := (<-capture.events).Data.(*events.ToolCallStartEvent)
	end := (<-capture.events).Data.(*events.ToolCallErrorEvent)
	if start.ToolCallID != "call1" || end.ToolCallID != start.ToolCallID || end.ToolName != "Read" {
		t.Fatalf("unpaired tools: %+v %+v", start, end)
	}
}

func TestRetainedProgressKeepsOnlyObservedBridgeReceipt(t *testing.T) {
	for _, observed := range []bool{true, false} {
		name := "transcript-only"
		if observed {
			name = "observed-bridge"
		}
		t.Run(name, func(t *testing.T) {
			capture := &retainedProgressCapture{events: make(chan *events.AgentEvent, 10)}
			agent := &Agent{
				sessionID: t.Name(), listeners: []AgentEventListener{capture},
				directToolExecutionEvents: observed,
				toolRegistry:              directToolRegistry(directToolFixture("execute_shell_command", "workspace")),
			}
			s := &Session{agent: agent, retainedActive: true, retainedSeq: 1}
			lifecycle := newCanonicalTurnLifecycle("")
			if observed {
				executor := agent.observedDirectToolExecutor("execute_shell_command", func(context.Context, map[string]interface{}) (string, error) {
					return "real stdout", nil
				})
				if _, err := executor(withCanonicalTurnLifecycle(t.Context(), lifecycle), map[string]interface{}{"command": "printf test"}); err != nil {
					t.Fatal(err)
				}
			}
			index := 0
			tools := map[string]retainedNativeTool{}
			// The transcript echoes a bridge call after it executed. A native
			// built-in tool must still produce its own pair in the next poll.
			for _, message := range []llmtypes.MessageContent{
				{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.ToolCall{ID: "bridge", FunctionCall: &llmtypes.FunctionCall{Name: "mcp__api_bridge__execute_shell_command", Arguments: `{}`}}}},
				{Role: llmtypes.ChatMessageTypeTool, Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: "bridge", Content: "real stdout"}}},
				{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.ToolCall{ID: "native", FunctionCall: &llmtypes.FunctionCall{Name: "Bash", Arguments: `{}`}}}},
				{Role: llmtypes.ChatMessageTypeTool, Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: "native", Content: "native stdout"}}},
			} {
				s.emitRetainedProgress(lifecycle, 1, llm.ProviderCodexCLI, func(llm.Provider, string) []llmtypes.MessageContent { return []llmtypes.MessageContent{message} }, &index, tools)
			}
			if got := len(capture.events); got != 4 {
				t.Fatalf("events = %d, want one bridge pair plus one native pair", got)
			}
			start := (<-capture.events).Data.(*events.ToolCallStartEvent)
			end := (<-capture.events).Data.(*events.ToolCallEndEvent)
			if start.ToolCallID != end.ToolCallID || end.Result != "real stdout" {
				t.Fatalf("bridge receipt = %+v %+v", start, end)
			}
			if observed && start.ServerName != "direct_execution" {
				t.Fatalf("expected authoritative bridge receipt, got %+v", start)
			}
			nativeStart := (<-capture.events).Data.(*events.ToolCallStartEvent)
			nativeEnd := (<-capture.events).Data.(*events.ToolCallEndEvent)
			if nativeStart.ToolName != "Bash" || nativeEnd.ToolCallID != "native" || nativeEnd.Result != "native stdout" {
				t.Fatalf("native receipt lost: %+v %+v", nativeStart, nativeEnd)
			}
			if len(tools) != 0 {
				t.Fatalf("completed tool calls retained: %+v", tools)
			}
		})
	}
}

func TestNativeInterruptClosesOnlyItsRetainedWatch(t *testing.T) {
	s, capture, cancel := newFollowupTestSession(t, func() string { return "" })
	defer cancel()
	s.agent.provider = llm.ProviderClaudeCode
	if _, err := s.ObserveNativeInput(t.Context(), "first", time.Now()); err != nil {
		t.Fatal(err)
	}
	ack := s.PrepareNativeInterrupt()
	if ack == nil {
		t.Fatal("no retained interrupt observer")
	}
	// A newer native submission supersedes the old watcher, even if it shares
	// the same canonical turn ID. The old input acknowledgement cannot close it.
	if _, err := s.ObserveNativeInput(t.Context(), "second", time.Now()); err != nil {
		t.Fatal(err)
	}
	ack()
	if !s.retainedActive {
		t.Fatal("late Ctrl+C acknowledgement cancelled newer input")
	}
	ack = s.PrepareNativeInterrupt()
	ack()
	completion := waitCompletion(t, capture)
	if completion.Status != "cancelled" || s.retainedActive || s.closed {
		t.Fatalf("bad native cancellation: %+v", completion)
	}
	if s.PrepareNativeInterrupt() != nil {
		t.Fatal("idle composer has no turn to cancel")
	}
}
