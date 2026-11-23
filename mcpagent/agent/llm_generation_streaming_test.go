package mcpagent

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	llm "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type recordingAgentEventListener struct {
	events []*events.AgentEvent
}

func (l *recordingAgentEventListener) HandleEvent(_ context.Context, event *events.AgentEvent) error {
	l.events = append(l.events, event)
	return nil
}

func (l *recordingAgentEventListener) Name() string {
	return "recording-agent-event-listener"
}

func TestStreamingManagerForwardsContentChunks(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID: "session-content-stream-test",
		listeners: []AgentEventListener{listener},
	}

	var callbackChunks []llmtypes.StreamChunk
	agent.streamingCallback = func(chunk llmtypes.StreamChunk) {
		callbackChunks = append(callbackChunks, chunk)
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 4),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "Hello "}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "world"}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: ""}
	close(sm.streamChan)
	<-sm.streamingDone

	if len(callbackChunks) != 2 {
		t.Fatalf("callback chunks = %d, want 2 (empty content skipped)", len(callbackChunks))
	}
	if callbackChunks[0].Content != "Hello " || callbackChunks[1].Content != "world" {
		t.Fatalf("callback content = %q + %q", callbackChunks[0].Content, callbackChunks[1].Content)
	}

	var chunkEvents int
	for _, e := range listener.events {
		if _, ok := e.Data.(*events.StreamingChunkEvent); ok {
			chunkEvents++
		}
	}
	if chunkEvents != 2 {
		t.Fatalf("StreamingChunkEvent count = %d, want 2", chunkEvents)
	}
	if sm.totalChunks != 2 {
		t.Fatalf("totalChunks = %d, want 2", sm.totalChunks)
	}
	if sm.contentChunkIndex != 2 {
		t.Fatalf("contentChunkIndex = %d, want 2", sm.contentChunkIndex)
	}
}

func TestStreamingManagerForwardsToolCallStartChunks(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID: "session-tool-start-test",
		listeners: []AgentEventListener{listener},
	}

	var callbackChunks []llmtypes.StreamChunk
	agent.streamingCallback = func(chunk llmtypes.StreamChunk) {
		callbackChunks = append(callbackChunks, chunk)
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 2),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	sm.streamChan <- llmtypes.StreamChunk{
		Type:       llmtypes.StreamChunkTypeToolCallStart,
		ToolName:   "bash",
		ToolCallID: "call_123",
		ToolArgs:   `{"command":"ls"}`,
	}
	close(sm.streamChan)
	<-sm.streamingDone

	if len(callbackChunks) != 0 {
		t.Fatalf("callback should NOT receive ToolCallStart, got %d chunks", len(callbackChunks))
	}

	var toolStartEvents int
	for _, e := range listener.events {
		if tse, ok := e.Data.(*events.ToolCallStartEvent); ok {
			toolStartEvents++
			if tse.ToolName != "bash" {
				t.Fatalf("ToolName = %q, want bash", tse.ToolName)
			}
			if tse.ToolCallID != "call_123" {
				t.Fatalf("ToolCallID = %q, want call_123", tse.ToolCallID)
			}
			if tse.ToolParams.Arguments != `{"command":"ls"}` {
				t.Fatalf("ToolParams.Arguments = %q", tse.ToolParams.Arguments)
			}
		}
	}
	if toolStartEvents != 1 {
		t.Fatalf("ToolCallStartEvent count = %d, want 1", toolStartEvents)
	}
}

func TestStreamingManagerForwardsToolCallEndChunks(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID: "session-tool-end-test",
		listeners: []AgentEventListener{listener},
	}

	var callbackChunks []llmtypes.StreamChunk
	agent.streamingCallback = func(chunk llmtypes.StreamChunk) {
		callbackChunks = append(callbackChunks, chunk)
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 2),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	sm.streamChan <- llmtypes.StreamChunk{
		Type:         llmtypes.StreamChunkTypeToolCallEnd,
		ToolName:     "bash",
		ToolCallID:   "call_456",
		ToolResult:   "file1.txt\nfile2.txt",
		ToolDuration: 250 * time.Millisecond,
	}
	close(sm.streamChan)
	<-sm.streamingDone

	if len(callbackChunks) != 1 {
		t.Fatalf("callback should receive ToolCallEnd, got %d chunks", len(callbackChunks))
	}
	if callbackChunks[0].ToolName != "bash" {
		t.Fatalf("callback ToolName = %q, want bash", callbackChunks[0].ToolName)
	}

	var toolEndEvents int
	for _, e := range listener.events {
		if tee, ok := e.Data.(*events.ToolCallEndEvent); ok {
			toolEndEvents++
			if tee.ToolName != "bash" {
				t.Fatalf("ToolName = %q, want bash", tee.ToolName)
			}
			if tee.ToolCallID != "call_456" {
				t.Fatalf("ToolCallID = %q, want call_456", tee.ToolCallID)
			}
			if tee.Result != "file1.txt\nfile2.txt" {
				t.Fatalf("Result = %q", tee.Result)
			}
		}
	}
	if toolEndEvents != 1 {
		t.Fatalf("ToolCallEndEvent count = %d, want 1", toolEndEvents)
	}

	if len(sm.CLIToolCalls) != 1 {
		t.Fatalf("CLIToolCalls = %d, want 1", len(sm.CLIToolCalls))
	}
	if sm.CLIToolCalls[0].ToolName != "bash" {
		t.Fatalf("CLIToolCalls[0].ToolName = %q, want bash", sm.CLIToolCalls[0].ToolName)
	}
}

func TestStreamingManagerMixedChunkFlow(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID: "session-mixed-flow-test",
		listeners: []AgentEventListener{listener},
	}

	var callbackTypes []llmtypes.StreamChunkType
	agent.streamingCallback = func(chunk llmtypes.StreamChunk) {
		callbackTypes = append(callbackTypes, chunk.Type)
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 8),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "I'll list files."}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallStart, ToolName: "bash", ToolCallID: "c1", ToolArgs: `{"cmd":"ls"}`}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallEnd, ToolName: "bash", ToolCallID: "c1", ToolResult: "a.txt", ToolDuration: 100 * time.Millisecond}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "Found a.txt."}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallStart, ToolName: "read", ToolCallID: "c2", ToolArgs: `{"path":"a.txt"}`}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallEnd, ToolName: "read", ToolCallID: "c2", ToolResult: "contents", ToolDuration: 50 * time.Millisecond}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "Done."}
	close(sm.streamChan)
	<-sm.streamingDone

	// Callback receives: Content, ToolCallEnd, Content, ToolCallEnd, Content (no ToolCallStart)
	wantCallbackTypes := []llmtypes.StreamChunkType{
		llmtypes.StreamChunkTypeContent,
		llmtypes.StreamChunkTypeToolCallEnd,
		llmtypes.StreamChunkTypeContent,
		llmtypes.StreamChunkTypeToolCallEnd,
		llmtypes.StreamChunkTypeContent,
	}
	if len(callbackTypes) != len(wantCallbackTypes) {
		t.Fatalf("callback count = %d, want %d: %v", len(callbackTypes), len(wantCallbackTypes), callbackTypes)
	}
	for i, wt := range wantCallbackTypes {
		if callbackTypes[i] != wt {
			t.Fatalf("callback[%d] type = %q, want %q", i, callbackTypes[i], wt)
		}
	}

	// Events: 3 StreamingChunk + 2 ToolCallStart + 2 ToolCallEnd = 7
	var chunkEvents, startEvents, endEvents int
	for _, e := range listener.events {
		switch e.Data.(type) {
		case *events.StreamingChunkEvent:
			chunkEvents++
		case *events.ToolCallStartEvent:
			startEvents++
		case *events.ToolCallEndEvent:
			endEvents++
		}
	}
	if chunkEvents != 3 {
		t.Fatalf("StreamingChunkEvent count = %d, want 3", chunkEvents)
	}
	if startEvents != 2 {
		t.Fatalf("ToolCallStartEvent count = %d, want 2", startEvents)
	}
	if endEvents != 2 {
		t.Fatalf("ToolCallEndEvent count = %d, want 2", endEvents)
	}

	if sm.totalChunks != 3 {
		t.Fatalf("totalChunks = %d, want 3 (content only)", sm.totalChunks)
	}
	if len(sm.CLIToolCalls) != 2 {
		t.Fatalf("CLIToolCalls = %d, want 2", len(sm.CLIToolCalls))
	}
}

func TestStreamingManagerForwardsTerminalChunks(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID: "session-terminal-stream-test",
		listeners: []AgentEventListener{
			listener,
		},
	}

	var callbackChunks []llmtypes.StreamChunk
	agent.streamingCallback = func(chunk llmtypes.StreamChunk) {
		callbackChunks = append(callbackChunks, chunk)
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 1),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	sm.streamChan <- llmtypes.StreamChunk{
		Type:     llmtypes.StreamChunkTypeTerminal,
		Content:  "Codex CLI screen snapshot",
		Metadata: map[string]interface{}{"tmux_session": "codex-pane-1"},
	}
	close(sm.streamChan)
	<-sm.streamingDone

	if len(callbackChunks) != 1 {
		t.Fatalf("callback chunks = %d, want 1", len(callbackChunks))
	}
	if callbackChunks[0].Type != llmtypes.StreamChunkTypeTerminal {
		t.Fatalf("callback chunk type = %q, want terminal", callbackChunks[0].Type)
	}

	if len(listener.events) != 1 {
		t.Fatalf("events = %d, want 1", len(listener.events))
	}
	chunkEvent, ok := listener.events[0].Data.(*events.StreamingChunkEvent)
	if !ok {
		t.Fatalf("event data type = %T, want *events.StreamingChunkEvent", listener.events[0].Data)
	}
	if chunkEvent.Content != "Codex CLI screen snapshot" {
		t.Fatalf("content = %q", chunkEvent.Content)
	}
	if got := chunkEvent.Metadata["kind"]; got != "terminal" {
		t.Fatalf("metadata kind = %v, want terminal", got)
	}
	if got := chunkEvent.Metadata["tmux_session"]; got != "codex-pane-1" {
		t.Fatalf("metadata tmux_session = %v, want codex-pane-1", got)
	}
	if sm.totalChunks != 1 {
		t.Fatalf("totalChunks = %d, want 1", sm.totalChunks)
	}
}

// TestStreamingManagerEmitsTerminalChunkEventEvenWhenSuppressEventsTrue
// locks in the regression for the production-config bug fixed in this
// commit's parent change.
//
// Background: the agentwrapper in mcp-agent-builder-go calls
// withGenerationStreamingEvents(false) (i.e. SuppressGenerationStreamingEvents
// = true) to keep per-token text-generation streaming events out of the
// chat event store. But terminal pane snapshots (StreamChunkTypeTerminal)
// are NOT generation events — they're a separate UX channel that the
// builder's terminal-pane store depends on. Without this gate carve-out,
// the terminal panel goes empty for every tmux-backed coding-agent call.
//
// This test fixes the production config to suppressEvents=true and
// proves a terminal chunk still produces a StreamingChunkEvent
// downstream. If anyone ever re-introduces a global suppressEvents gate
// over the terminal branch, this test fails loudly.
func TestStreamingManagerEmitsTerminalChunkEventEvenWhenSuppressEventsTrue(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID: "session-terminal-suppress-events-test",
		listeners: []AgentEventListener{listener},
	}

	sm := &streamingManager{
		streamChan:     make(chan llmtypes.StreamChunk, 1),
		streamingDone:  make(chan bool, 1),
		startTime:      time.Now(),
		suppressEvents: true, // production wrapper config
	}
	go sm.processChunks(context.Background(), agent)

	sm.streamChan <- llmtypes.StreamChunk{
		Type:    llmtypes.StreamChunkTypeTerminal,
		Content: "Codex CLI pane snapshot",
		Metadata: map[string]interface{}{
			"tmux_session": "codex-pane-suppress-test",
		},
	}
	close(sm.streamChan)
	<-sm.streamingDone

	if len(listener.events) != 1 {
		t.Fatalf("expected 1 event with suppressEvents=true (terminal chunks must bypass the gate); got %d", len(listener.events))
	}
	chunkEvent, ok := listener.events[0].Data.(*events.StreamingChunkEvent)
	if !ok {
		t.Fatalf("event data type = %T, want *events.StreamingChunkEvent", listener.events[0].Data)
	}
	if chunkEvent.Content != "Codex CLI pane snapshot" {
		t.Fatalf("content = %q, want %q", chunkEvent.Content, "Codex CLI pane snapshot")
	}
	if got := chunkEvent.Metadata["kind"]; got != "terminal" {
		t.Fatalf("metadata kind = %v, want terminal", got)
	}
	if got := chunkEvent.Metadata["tmux_session"]; got != "codex-pane-suppress-test" {
		t.Fatalf("metadata tmux_session = %v, want codex-pane-suppress-test", got)
	}
}

// TestStreamingManagerSuppressesContentChunksWhenSuppressEventsTrue is
// the inverse assertion: text-content chunks ARE still suppressed when
// the flag is on. Together with the terminal-bypass test above, this
// pins down exactly what the suppressEvents gate covers.
func TestStreamingManagerSuppressesContentChunksWhenSuppressEventsTrue(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID: "session-content-suppress-events-test",
		listeners: []AgentEventListener{listener},
	}

	sm := &streamingManager{
		streamChan:     make(chan llmtypes.StreamChunk, 1),
		streamingDone:  make(chan bool, 1),
		startTime:      time.Now(),
		suppressEvents: true,
	}
	go sm.processChunks(context.Background(), agent)

	sm.streamChan <- llmtypes.StreamChunk{
		Type:    llmtypes.StreamChunkTypeContent,
		Content: "this should be suppressed",
	}
	close(sm.streamChan)
	<-sm.streamingDone

	for _, ev := range listener.events {
		if _, ok := ev.Data.(*events.StreamingChunkEvent); ok {
			t.Fatalf("expected no StreamingChunkEvent for content chunks under suppressEvents=true; got %+v", ev)
		}
	}
}

// TestStreamingManagerChunkRoutingMatrixProductionConfig is the
// canonical, single-source-of-truth pin for what suppressEvents=true
// (the production wrapper config) does to each chunk type. Every chunk
// type is fed through processChunks, and the test asserts:
//
//   - Content   → callback fires; NO StreamingChunkEvent
//   - Terminal  → callback fires; StreamingChunkEvent IS emitted (bypass)
//   - ToolStart → callback does NOT fire; ToolCallStartEvent IS emitted
//   - ToolEnd   → callback fires; ToolCallEndEvent IS emitted
//
// This matrix is what was missed by the 100+ tests when the
// terminal-snapshot regression (commit 298c1b66) shipped. Any future
// change to the chunk routing must update this test or break it loudly.
func TestStreamingManagerChunkRoutingMatrixProductionConfig(t *testing.T) {
	cases := []struct {
		name             string
		chunk            llmtypes.StreamChunk
		wantCallback     bool
		wantChunkEvent   bool   // StreamingChunkEvent expected?
		wantToolStartEvt bool   // ToolCallStartEvent expected?
		wantToolEndEvt   bool   // ToolCallEndEvent expected?
		wantChunkKind    string // metadata["kind"] check (if wantChunkEvent)
	}{
		{
			name:           "content_suppressed_under_production_config",
			chunk:          llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "hi"},
			wantCallback:   true,
			wantChunkEvent: false,
		},
		{
			name:           "terminal_bypasses_suppress_gate",
			chunk:          llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeTerminal, Content: "pane snapshot"},
			wantCallback:   true,
			wantChunkEvent: true,
			wantChunkKind:  "terminal",
		},
		{
			name:             "tool_call_start_unaffected_by_suppress",
			chunk:            llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallStart, ToolName: "Bash", ToolCallID: "t1", ToolArgs: "{}"},
			wantCallback:     false,
			wantToolStartEvt: true,
		},
		{
			name:           "tool_call_end_unaffected_by_suppress",
			chunk:          llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallEnd, ToolName: "Bash", ToolCallID: "t1", ToolArgs: "{}", ToolResult: "ok"},
			wantCallback:   true,
			wantToolEndEvt: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			listener := &recordingAgentEventListener{}
			var callbackChunks []llmtypes.StreamChunk
			agent := &Agent{
				sessionID: "session-chunk-routing-matrix-" + tc.name,
				listeners: []AgentEventListener{listener},
				streamingCallback: func(c llmtypes.StreamChunk) {
					callbackChunks = append(callbackChunks, c)
				},
			}

			sm := &streamingManager{
				streamChan:     make(chan llmtypes.StreamChunk, 1),
				streamingDone:  make(chan bool, 1),
				startTime:      time.Now(),
				suppressEvents: true, // production wrapper config
			}
			go sm.processChunks(context.Background(), agent)

			sm.streamChan <- tc.chunk
			close(sm.streamChan)
			<-sm.streamingDone

			gotCallback := len(callbackChunks) > 0
			if gotCallback != tc.wantCallback {
				t.Fatalf("callback fired = %v, want %v (chunks=%+v)", gotCallback, tc.wantCallback, callbackChunks)
			}

			var gotChunkEvt, gotToolStart, gotToolEnd bool
			var chunkKind string
			for _, ev := range listener.events {
				switch d := ev.Data.(type) {
				case *events.StreamingChunkEvent:
					gotChunkEvt = true
					if d.Metadata != nil {
						if k, ok := d.Metadata["kind"].(string); ok {
							chunkKind = k
						}
					}
				case *events.ToolCallStartEvent:
					gotToolStart = true
				case *events.ToolCallEndEvent:
					gotToolEnd = true
				}
			}
			if gotChunkEvt != tc.wantChunkEvent {
				t.Fatalf("StreamingChunkEvent emitted = %v, want %v", gotChunkEvt, tc.wantChunkEvent)
			}
			if gotToolStart != tc.wantToolStartEvt {
				t.Fatalf("ToolCallStartEvent emitted = %v, want %v", gotToolStart, tc.wantToolStartEvt)
			}
			if gotToolEnd != tc.wantToolEndEvt {
				t.Fatalf("ToolCallEndEvent emitted = %v, want %v", gotToolEnd, tc.wantToolEndEvt)
			}
			if tc.wantChunkKind != "" && chunkKind != tc.wantChunkKind {
				t.Fatalf("StreamingChunkEvent metadata kind = %q, want %q", chunkKind, tc.wantChunkKind)
			}
		})
	}
}

func TestStreamingManagerPromotesNestedCLIToolFailureToErrorEvent(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID: "session-cli-nested-tool-error",
		listeners: []AgentEventListener{listener},
	}
	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 1),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)
	sm.streamChan <- llmtypes.StreamChunk{
		Type:         llmtypes.StreamChunkTypeToolCallEnd,
		ToolName:     "execute_shell_command",
		ToolCallID:   "nested-failure-1",
		ToolResult:   `{"content":[{"text":"{\"stdout\":\"\",\"stderr\":\"authorization denied\",\"exit_code\":14}"}]}`,
		ToolDuration: 25 * time.Millisecond,
	}
	close(sm.streamChan)
	<-sm.streamingDone

	var errorCount, endCount int
	for _, event := range listener.events {
		switch data := event.Data.(type) {
		case *events.ToolCallErrorEvent:
			errorCount++
			if data.ToolCallID != "nested-failure-1" {
				t.Fatalf("error event tool call ID = %q", data.ToolCallID)
			}
		case *events.ToolCallEndEvent:
			endCount++
		}
	}
	if errorCount != 1 || endCount != 0 {
		t.Fatalf("error events=%d end events=%d, want 1/0", errorCount, endCount)
	}
	if len(sm.CLIToolCalls) != 1 {
		t.Fatalf("CLI tool history length=%d, want 1", len(sm.CLIToolCalls))
	}
}

func TestStreamingManagerDrainsAfterContextCancel(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID: "session-ctx-cancel-test",
		listeners: []AgentEventListener{listener},
	}

	var callbackChunks []llmtypes.StreamChunk
	agent.streamingCallback = func(chunk llmtypes.StreamChunk) {
		callbackChunks = append(callbackChunks, chunk)
	}

	ctx, cancel := context.WithCancel(context.Background())

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 8),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(ctx, agent)

	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "before cancel"}
	cancel()
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "after cancel"}
	close(sm.streamChan)

	select {
	case <-sm.streamingDone:
	case <-time.After(2 * time.Second):
		t.Fatal("streamingDone not signalled within 2s after context cancel + channel close")
	}

	if len(callbackChunks) < 1 {
		t.Fatalf("expected at least 1 callback chunk, got %d", len(callbackChunks))
	}
	if callbackChunks[0].Content != "before cancel" {
		t.Fatalf("first chunk = %q, want 'before cancel'", callbackChunks[0].Content)
	}
}

func TestStreamingManagerCLIToolCallsAccumulation(t *testing.T) {
	agent := &Agent{
		sessionID: "session-tool-accumulation-test",
		listeners: []AgentEventListener{&recordingAgentEventListener{}},
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 8),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	tools := []struct {
		name   string
		callID string
		result string
	}{
		{"bash", "c1", "output1"},
		{"read", "c2", "file contents"},
		{"bash", "c3", "output2"},
	}

	for _, tc := range tools {
		sm.streamChan <- llmtypes.StreamChunk{
			Type:         llmtypes.StreamChunkTypeToolCallEnd,
			ToolName:     tc.name,
			ToolCallID:   tc.callID,
			ToolResult:   tc.result,
			ToolDuration: 100 * time.Millisecond,
		}
	}
	close(sm.streamChan)
	<-sm.streamingDone

	if len(sm.CLIToolCalls) != 3 {
		t.Fatalf("CLIToolCalls = %d, want 3", len(sm.CLIToolCalls))
	}
	for i, tc := range tools {
		if sm.CLIToolCalls[i].ToolName != tc.name {
			t.Fatalf("CLIToolCalls[%d].ToolName = %q, want %q", i, sm.CLIToolCalls[i].ToolName, tc.name)
		}
		if sm.CLIToolCalls[i].ToolCallID != tc.callID {
			t.Fatalf("CLIToolCalls[%d].ToolCallID = %q, want %q", i, sm.CLIToolCalls[i].ToolCallID, tc.callID)
		}
		if sm.CLIToolCalls[i].ToolResult != tc.result {
			t.Fatalf("CLIToolCalls[%d].ToolResult = %q, want %q", i, sm.CLIToolCalls[i].ToolResult, tc.result)
		}
	}
}

func TestStreamingManagerEmptyContentSkipped(t *testing.T) {
	agent := &Agent{
		sessionID: "session-empty-content-test",
		listeners: []AgentEventListener{&recordingAgentEventListener{}},
	}

	var callbackCount int
	agent.streamingCallback = func(_ llmtypes.StreamChunk) {
		callbackCount++
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 4),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: ""}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeTerminal, Content: ""}
	close(sm.streamChan)
	<-sm.streamingDone

	if callbackCount != 0 {
		t.Fatalf("callbackCount = %d, want 0 (all empty content should be skipped)", callbackCount)
	}
	if sm.totalChunks != 0 {
		t.Fatalf("totalChunks = %d, want 0", sm.totalChunks)
	}
}

func TestFinishStreamingEmitsEndEvent(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID: "session-finish-streaming-test",
		modelID:   "claude-opus-4-6",
		listeners: []AgentEventListener{listener},
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 4),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "Hello"}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: " world"}

	totalTokens := 42
	cachedTokens := 10
	resp := &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{
			{
				Content:    "Hello world",
				StopReason: "end_turn",
				GenerationInfo: &llmtypes.GenerationInfo{
					TotalTokens:         &totalTokens,
					CachedContentTokens: &cachedTokens,
					Additional: map[string]interface{}{
						"claude_code_model": "claude-opus-4-6",
					},
				},
			},
		},
	}

	agent.finishStreaming(context.Background(), sm, resp)

	var endEvent *events.StreamingEndEvent
	for _, e := range listener.events {
		if ee, ok := e.Data.(*events.StreamingEndEvent); ok {
			endEvent = ee
		}
	}
	if endEvent == nil {
		t.Fatal("StreamingEndEvent not emitted by finishStreaming")
	}
	if endEvent.TotalChunks != 2 {
		t.Fatalf("TotalChunks = %d, want 2", endEvent.TotalChunks)
	}
	if endEvent.TotalTokens != 42 {
		t.Fatalf("TotalTokens = %d, want 42", endEvent.TotalTokens)
	}
	if endEvent.FinishReason != "end_turn" {
		t.Fatalf("FinishReason = %q, want end_turn", endEvent.FinishReason)
	}
	if endEvent.ResolvedModel != "claude-opus-4-6" {
		t.Fatalf("ResolvedModel = %q, want claude-opus-4-6", endEvent.ResolvedModel)
	}
	if endEvent.CacheTokens != 10 {
		t.Fatalf("CacheTokens = %d, want 10", endEvent.CacheTokens)
	}
}

func TestFinishStreamingTerminalEndIncludesRetentionMetadata(t *testing.T) {
	tests := []struct {
		provider     string
		retentionKey string
		sessionKey   string
	}{
		{provider: "claude-code", retentionKey: "claude_code_interactive_retention_seconds", sessionKey: "claude_code_interactive_session"},
		{provider: "codex-cli", retentionKey: "codex_interactive_retention_seconds", sessionKey: "codex_interactive_session"},
		{provider: "cursor-cli", retentionKey: "cursor_interactive_retention_seconds", sessionKey: "cursor_interactive_session"},
		{provider: "pi-cli", retentionKey: "pi_interactive_retention_seconds", sessionKey: "pi_interactive_session"},
	}

	for _, tc := range tests {
		t.Run(tc.provider, func(t *testing.T) {
			listener := &recordingAgentEventListener{}
			agent := &Agent{
				sessionID: "session-terminal-retention-test-" + tc.provider,
				provider:  llm.Provider(tc.provider),
				listeners: []AgentEventListener{listener},
			}
			sm := &streamingManager{
				streamChan:    make(chan llmtypes.StreamChunk, 2),
				streamingDone: make(chan bool, 1),
				startTime:     time.Now(),
			}
			go sm.processChunks(context.Background(), agent)
			sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeTerminal, Content: "terminal screen"}

			legacyTmux := "legacy-" + tc.provider
			typedTmux := "typed-" + tc.provider
			fallbackInfo := &llmtypes.GenerationInfo{Additional: map[string]interface{}{tc.sessionKey: legacyTmux}}
			if got := terminalTmuxSessionFromGenerationInfo(fallbackInfo); got != legacyTmux {
				t.Fatalf("provider-key fallback tmux = %q, want %q", got, legacyTmux)
			}
			genInfo := &llmtypes.GenerationInfo{Additional: map[string]interface{}{
				tc.sessionKey:   legacyTmux,
				tc.retentionKey: 300,
			}}
			llmtypes.AttachCodingProviderSessionHandle(genInfo, llmtypes.CodingProviderSessionHandle{
				Provider:    tc.provider,
				Transport:   llmtypes.CodingProviderTransportTmux,
				TmuxSession: typedTmux,
			})
			resp := &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{
				Content:        "done",
				GenerationInfo: genInfo,
			}}}

			agent.finishStreaming(context.Background(), sm, resp)

			var endEvent *events.StreamingEndEvent
			for _, e := range listener.events {
				if ee, ok := e.Data.(*events.StreamingEndEvent); ok {
					endEvent = ee
				}
			}
			if endEvent == nil {
				t.Fatal("StreamingEndEvent not emitted")
			}
			if endEvent.Metadata["kind"] != "terminal" {
				t.Fatalf("metadata kind = %v, want terminal", endEvent.Metadata["kind"])
			}
			if endEvent.Metadata["terminal_retention_seconds"] != 300 {
				t.Fatalf("retention metadata = %v, want 300", endEvent.Metadata["terminal_retention_seconds"])
			}
			if endEvent.Metadata["tmux_session"] != typedTmux {
				t.Fatalf("tmux_session metadata = %v, want typed handle %q", endEvent.Metadata["tmux_session"], typedTmux)
			}
		})
	}
}

func TestFinishStreamingSafeDoubleClose(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID: "session-double-close-test",
		listeners: []AgentEventListener{listener},
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 2),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	// Simulate adapter closing the channel (normal flow)
	close(sm.streamChan)

	// finishStreaming does a safe double-close (recovered) then waits for processChunks to exit
	agent.finishStreaming(context.Background(), sm, nil)

	var endEvents int
	for _, e := range listener.events {
		if _, ok := e.Data.(*events.StreamingEndEvent); ok {
			endEvents++
		}
	}
	if endEvents != 1 {
		t.Fatalf("StreamingEndEvent count = %d, want 1 (should not panic on double close)", endEvents)
	}
}

func TestFinishStreamingNilManager(t *testing.T) {
	agent := &Agent{
		sessionID: "session-nil-sm-test",
		listeners: []AgentEventListener{&recordingAgentEventListener{}},
	}
	agent.finishStreaming(context.Background(), nil, nil)
}

func TestFinishStreamingNilResponse(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID: "session-nil-resp-test",
		listeners: []AgentEventListener{listener},
	}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 1),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	agent.finishStreaming(context.Background(), sm, nil)

	var endEvent *events.StreamingEndEvent
	for _, e := range listener.events {
		if ee, ok := e.Data.(*events.StreamingEndEvent); ok {
			endEvent = ee
		}
	}
	if endEvent == nil {
		t.Fatal("StreamingEndEvent not emitted even with nil response")
	}
	if endEvent.TotalTokens != 0 {
		t.Fatalf("TotalTokens = %d, want 0 (nil resp)", endEvent.TotalTokens)
	}
	if endEvent.ResolvedModel != "" {
		t.Fatalf("ResolvedModel = %q, want empty (nil resp)", endEvent.ResolvedModel)
	}
}

// TestStreamingManagerLabelsChunkSource proves processChunks stamps
// StreamingChunkEvent.Source so a no-terminal UI can select transcript-only
// content without heuristics: transcript-tailed content -> "transcript", plain
// content -> "content", terminal snapshots -> "terminal". Regression guard for the
// real-bridge finding that terminal frames were indistinguishable from clean text.
func TestStreamingManagerLabelsChunkSource(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{sessionID: "session-source-label-test", listeners: []AgentEventListener{listener}}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 4),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	sm.streamChan <- llmtypes.StreamChunk{
		Type:     llmtypes.StreamChunkTypeContent,
		Content:  "I'll read the file.",
		Metadata: map[string]interface{}{"claude_code_stream_source": "transcript"},
	}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "plain content"}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeTerminal, Content: "\x1b[2J[raw pane frame]"}
	close(sm.streamChan)
	<-sm.streamingDone

	var sources []string
	var noTerminal []string
	for _, e := range listener.events {
		sc, ok := e.Data.(*events.StreamingChunkEvent)
		if !ok {
			continue
		}
		sources = append(sources, sc.Source)
		if sc.Source != events.StreamingChunkSourceTerminal {
			noTerminal = append(noTerminal, sc.Content)
		}
	}

	want := []string{
		events.StreamingChunkSourceTranscript,
		events.StreamingChunkSourceContent,
		events.StreamingChunkSourceTerminal,
	}
	if !reflect.DeepEqual(sources, want) {
		t.Fatalf("chunk sources = %v, want %v", sources, want)
	}
	// A no-terminal UI drops Source=="terminal" and gets ONLY the clean text.
	if len(noTerminal) != 2 || noTerminal[0] != "I'll read the file." || noTerminal[1] != "plain content" {
		t.Fatalf("no-terminal view = %v, want the two clean content chunks", noTerminal)
	}
}

// TestStreamingManagerPropagatesDeltaMarker proves processChunks carries the
// token-level DELTA marker onto StreamingChunkEvent.IsDelta, so a mcpagent-layer
// consumer can reassemble pi's mid-word deltas without garbling them (the
// real-bridge review finding). Regression guard.
func TestStreamingManagerPropagatesDeltaMarker(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{sessionID: "session-delta-marker-test", listeners: []AgentEventListener{listener}}

	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 8),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)

	delta := func(s string) llmtypes.StreamChunk {
		return llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: s, Metadata: map[string]interface{}{llmtypes.ContentDeltaMetadataKey: true}}
	}
	// A pi-style token split mid-word across two deltas, then a block chunk.
	sm.streamChan <- delta("The id is PI_STREAM_B_cea332")
	sm.streamChan <- delta("c7")
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: "Done."}
	sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeTerminal, Content: "\x1b[2J[pane]"}
	close(sm.streamChan)
	<-sm.streamingDone

	var evs []*events.StreamingChunkEvent
	for _, e := range listener.events {
		if sc, ok := e.Data.(*events.StreamingChunkEvent); ok {
			evs = append(evs, sc)
		}
	}
	if len(evs) != 4 {
		t.Fatalf("got %d StreamingChunkEvents, want 4", len(evs))
	}
	wantDelta := []bool{true, true, false, false}
	for i, sc := range evs {
		if sc.IsDelta != wantDelta[i] {
			t.Fatalf("event[%d] (%q) IsDelta=%v, want %v", i, sc.Content, sc.IsDelta, wantDelta[i])
		}
	}

	// A consumer reassembles using IsDelta: concatenate delta runs verbatim, join
	// blocks with "\n", drop terminal frames. The mid-word token must survive.
	var blocks []string
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			blocks = append(blocks, buf.String())
			buf.Reset()
		}
	}
	for _, sc := range evs {
		if sc.Source == events.StreamingChunkSourceTerminal {
			continue
		}
		if sc.IsDelta {
			buf.WriteString(sc.Content)
			continue
		}
		flush()
		blocks = append(blocks, sc.Content)
	}
	flush()
	msg := strings.Join(blocks, "\n")
	if !strings.Contains(msg, "PI_STREAM_B_cea332c7") {
		t.Fatalf("delta reassembly garbled the token: %q", msg)
	}
	if msg != "The id is PI_STREAM_B_cea332c7\nDone." {
		t.Fatalf("unexpected reassembled message: %q", msg)
	}
}

// PLAT-024: 35 of 90 sampled "[TOOL_ERROR] CLI tool payload failure" markers
// carried tool="" on 2026-08-04 — the CLI stream occasionally loses the tool
// name between its ToolCallStart and ToolCallEnd chunks, and the marker
// logged whatever chunk.ToolName held at End time with no fallback. This is
// table-driven per PLAT-024's acceptance: a stable name when one can be
// proven (via the call's own start event, then a narrow envelope pattern),
// and an explicit "unknown" — never a silent blank — when neither can.
func TestStreamingManagerRecoversToolNameForUnattributedErrorEvent(t *testing.T) {
	tests := []struct {
		name       string
		chunks     []llmtypes.StreamChunk
		wantTool   string
		wantReason string
	}{
		{
			name: "recovers the name from this call's own start event",
			chunks: []llmtypes.StreamChunk{
				{Type: llmtypes.StreamChunkTypeToolCallStart, ToolName: "diff_patch_workspace_file", ToolCallID: "call-1"},
				{
					Type: llmtypes.StreamChunkTypeToolCallEnd, ToolName: "", ToolCallID: "call-1",
					ToolResult: `{"stdout":"ERROR: tool execution failed: decisions[0] contains unknown field","exit_code":0}`,
				},
			},
			wantTool:   "diff_patch_workspace_file",
			wantReason: "structured call identity (start event) must win when available",
		},
		{
			name: "falls back to the narrow envelope pattern with no start event on record",
			chunks: []llmtypes.StreamChunk{
				{
					Type: llmtypes.StreamChunkTypeToolCallEnd, ToolName: "", ToolCallID: "call-2",
					ToolResult: `{"stdout":"ERROR: tool execution failed: layer=custom_tool_handler tool=record_pulse_result session=abc: reason","exit_code":0}`,
				},
			},
			wantTool:   "record_pulse_result",
			wantReason: "envelope fallback only, since no start event was ever recorded for call-2",
		},
		{
			name: "emits unknown rather than blank when neither source can prove a name",
			chunks: []llmtypes.StreamChunk{
				{
					Type: llmtypes.StreamChunkTypeToolCallEnd, ToolName: "", ToolCallID: "call-3",
					ToolResult: `{"stdout":"","stderr":"authorization denied","exit_code":14}`,
				},
			},
			wantTool:   "unknown",
			wantReason: "no start event, no matching envelope phrase — must not go out blank",
		},
		{
			name: "the transport wrapper's own name wins over a nested tool name in its result",
			chunks: []llmtypes.StreamChunk{
				{Type: llmtypes.StreamChunkTypeToolCallStart, ToolName: "execute_shell_command", ToolCallID: "call-4"},
				{
					Type: llmtypes.StreamChunkTypeToolCallEnd, ToolName: "execute_shell_command", ToolCallID: "call-4",
					ToolResult: `{"stdout":"ERROR: tool execution failed: layer=custom_tool_handler tool=record_pulse_impact session=abc","exit_code":0}`,
				},
			},
			wantTool:   "execute_shell_command",
			wantReason: "chunk.ToolName was non-empty; the nested tool name must not hijack attribution",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listener := &recordingAgentEventListener{}
			agent := &Agent{sessionID: "session-tool-name-recovery", listeners: []AgentEventListener{listener}}
			sm := &streamingManager{
				streamChan:    make(chan llmtypes.StreamChunk, len(tt.chunks)),
				streamingDone: make(chan bool, 1),
				startTime:     time.Now(),
			}
			go sm.processChunks(context.Background(), agent)
			for _, chunk := range tt.chunks {
				sm.streamChan <- chunk
			}
			close(sm.streamChan)
			<-sm.streamingDone

			var got string
			var found bool
			for _, event := range listener.events {
				if data, ok := event.Data.(*events.ToolCallErrorEvent); ok {
					got = data.ToolName
					found = true
				}
			}
			if !found {
				t.Fatalf("no ToolCallErrorEvent emitted")
			}
			if got != tt.wantTool {
				t.Fatalf("tool name = %q, want %q (%s)", got, tt.wantTool, tt.wantReason)
			}
		})
	}
}

func TestStreamingManagerRecoversToolNameBeforeFailureClassification(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{sessionID: "session-tool-name-classification", listeners: []AgentEventListener{listener}}
	sm := &streamingManager{
		streamChan:    make(chan llmtypes.StreamChunk, 2),
		streamingDone: make(chan bool, 1),
		startTime:     time.Now(),
	}
	go sm.processChunks(context.Background(), agent)
	sm.streamChan <- llmtypes.StreamChunk{
		Type:       llmtypes.StreamChunkTypeToolCallStart,
		ToolName:   "query_workflow_db",
		ToolCallID: "call-domain-record",
	}
	sm.streamChan <- llmtypes.StreamChunk{
		Type:       llmtypes.StreamChunkTypeToolCallEnd,
		ToolName:   "",
		ToolCallID: "call-domain-record",
		ToolResult: `{"status":"failed","detail":"this is a stored workflow record, not an executor failure"}`,
	}
	close(sm.streamChan)
	<-sm.streamingDone

	var endName string
	for _, event := range listener.events {
		switch data := event.Data.(type) {
		case *events.ToolCallErrorEvent:
			t.Fatalf("legitimate query_workflow_db domain data was promoted to an error after name loss: %#v", data)
		case *events.ToolCallEndEvent:
			endName = data.ToolName
		}
	}
	if endName != "query_workflow_db" {
		t.Fatalf("ToolCallEnd name = %q, want recovered query_workflow_db", endName)
	}
	if len(sm.CLIToolCalls) != 1 || sm.CLIToolCalls[0].ToolName != "query_workflow_db" {
		t.Fatalf("CLI history did not retain the recovered name: %#v", sm.CLIToolCalls)
	}
}

// Native framing survives the wrapper without becoming a turn completion or
// a second content delta; per-block indices keep existing prefix replay valid.
func TestStreamingManagerForwardsNativeAssistantBlocks(t *testing.T) {
	listener := &recordingAgentEventListener{}
	a := &Agent{sessionID: "native-block-chat", listeners: []AgentEventListener{listener}}
	sm := &streamingManager{streamChan: make(chan llmtypes.StreamChunk, 8), streamingDone: make(chan bool, 1)}
	go sm.processChunks(context.Background(), a)
	for _, block := range []string{"a", "b"} {
		meta := map[string]interface{}{llmtypes.AssistantBlockIDMetadataKey: block, llmtypes.AssistantBlockPhaseMetadataKey: "start", "unrelated": "not forwarded"}
		sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeAssistantBlock, Metadata: meta}
		sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: block, Metadata: map[string]interface{}{llmtypes.AssistantBlockIDMetadataKey: block, llmtypes.ContentDeltaMetadataKey: true, "unrelated": "not forwarded"}}
		sm.streamChan <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeAssistantBlock, Content: block, Metadata: map[string]interface{}{llmtypes.AssistantBlockIDMetadataKey: block, llmtypes.AssistantBlockPhaseMetadataKey: "end"}}
	}
	close(sm.streamChan)
	<-sm.streamingDone
	if len(listener.events) != 6 {
		t.Fatalf("events=%d", len(listener.events))
	}
	for i, id := range []string{"a", "b"} {
		start, ok := listener.events[3*i].Data.(*events.StreamingStartEvent)
		if !ok || start.Metadata[llmtypes.AssistantBlockIDMetadataKey] != id || start.Metadata["unrelated"] != nil {
			t.Fatalf("start=%+v", start)
		}
		delta := listener.events[3*i+1].Data.(*events.StreamingChunkEvent)
		end := listener.events[3*i+2].Data.(*events.StreamingChunkEvent)
		if delta.ChunkIndex != 1 || !delta.IsDelta || delta.Metadata[llmtypes.AssistantBlockIDMetadataKey] != id || delta.Metadata["unrelated"] != nil {
			t.Fatalf("delta=%+v", delta)
		}
		if end.Source != events.StreamingChunkSourceTranscript || end.IsDelta || end.Content != id || end.Metadata[llmtypes.AssistantBlockPhaseMetadataKey] != "end" {
			t.Fatalf("end=%+v", end)
		}
	}
}

// PLAT-746: an objective is private chat state, never assistant reply text,
// token usage or a generic account status snapshot.
func TestNativeGoalStreamStaysOutOfContentAndUsage(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{sessionID: "goal-chat", listeners: []AgentEventListener{listener}}
	manager := &streamingManager{streamChan: make(chan llmtypes.StreamChunk, 2), streamingDone: make(chan bool, 1), startTime: time.Now()}
	go manager.processChunks(context.Background(), agent)
	manager.streamChan <- llmtypes.NativeGoalChunk(llmtypes.NativeGoalUpdate{Provider: "codex-cli", NativeSessionID: "native-thread", Goal: &llmtypes.NativeGoal{Objective: "private objective", Status: "active"}})
	manager.streamChan <- llmtypes.NativeGoalChunk(llmtypes.NativeGoalUpdate{Provider: "codex-cli", NativeSessionID: "native-thread", Goal: nil})
	close(manager.streamChan)
	<-manager.streamingDone
	goals := []*events.NativeGoalEvent{}
	for _, event := range listener.events {
		if goal, ok := event.Data.(*events.NativeGoalEvent); ok {
			goals = append(goals, goal)
		} else {
			t.Fatalf("goal became another event: %s", event.Type)
		}
	}
	if len(goals) != 2 || goals[0].Update.NativeSessionID != "native-thread" || goals[0].Update.Goal.Objective != "private objective" || goals[1].Update.Goal != nil {
		t.Fatal("native goal update/clear lost")
	}
	if manager.totalChunks != 0 || manager.contentChunkIndex != 0 {
		t.Fatal("goal counted as assistant content")
	}
}
