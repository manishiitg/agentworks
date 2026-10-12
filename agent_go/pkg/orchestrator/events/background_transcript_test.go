package events

import (
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
)

func TestBackgroundAgentTranscriptPathIsRootedBelowConversationStorage(t *testing.T) {
	got := BackgroundAgentTranscriptPath("workspace/social-media", "session-1", "workshop-background-task-abc")
	want := "workspace/social-media/builder/conversation/background/session-1/workshop-background-task-abc.json"
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestBackgroundAgentTranscriptPathTrimsWorkspaceSlashes(t *testing.T) {
	got := BackgroundAgentTranscriptPath("/workspace/social-media/", "session-1", "agent-1")
	if strings.HasPrefix(got, "/") {
		t.Fatalf("path retained a leading slash: %q", got)
	}
	if !strings.HasPrefix(got, "workspace/social-media/builder/conversation/background/") {
		t.Fatalf("path = %q, want workspace-rooted background subtree", got)
	}
}

func TestBackgroundAgentTranscriptRoundTripsThroughMarshalAndParse(t *testing.T) {
	started := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	transcript := NewBackgroundAgentTranscript("session-1", "agent-1", "parent-exec-1", "Measurement Validator", "workshop_background", started)
	if transcript.Status != "running" {
		t.Fatalf("initial status = %q, want running", transcript.Status)
	}

	transcript.AppendEvent(BackgroundAgentTranscriptEvent{
		Timestamp: started.Add(time.Second),
		Type:      backgroundTranscriptEventUserMessage,
		Role:      "user",
		Text:      "run the validator",
	})
	transcript.AppendEvent(BackgroundAgentTranscriptEvent{
		Timestamp: started.Add(2 * time.Second),
		Type:      backgroundTranscriptEventToolCall,
		ToolCall:  &events.ToolCallRecord{ToolCallID: "tc-1", ToolName: "read_file", Arguments: `{"path":"a.txt"}`, Status: "running"},
	})

	completed := started.Add(5 * time.Second)
	transcript.MarkTerminal("failed", "provider timeout", completed)

	content, err := transcript.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	parsed, err := ParseBackgroundAgentTranscript(content)
	if err != nil {
		t.Fatalf("ParseBackgroundAgentTranscript: %v", err)
	}
	if parsed == nil {
		t.Fatal("parsed transcript is nil")
	}
	if parsed.Status != "failed" {
		t.Fatalf("status = %q, want failed", parsed.Status)
	}
	if parsed.Error != "provider timeout" {
		t.Fatalf("error = %q, want %q", parsed.Error, "provider timeout")
	}
	if !parsed.CompletedAt.Equal(completed) {
		t.Fatalf("completed_at = %v, want %v", parsed.CompletedAt, completed)
	}
	if len(parsed.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(parsed.Events))
	}
	if parsed.Events[0].Text != "run the validator" {
		t.Fatalf("events[0].Text = %q, want the user prompt", parsed.Events[0].Text)
	}
	if parsed.Events[1].ToolCall == nil || parsed.Events[1].ToolCall.ToolCallID != "tc-1" {
		t.Fatalf("events[1].ToolCall = %+v, want tool call tc-1", parsed.Events[1].ToolCall)
	}
}

func TestParseBackgroundAgentTranscriptEmptyContentIsNotAnError(t *testing.T) {
	transcript, err := ParseBackgroundAgentTranscript("")
	if err != nil {
		t.Fatalf("ParseBackgroundAgentTranscript(\"\"): %v", err)
	}
	if transcript != nil {
		t.Fatalf("transcript = %+v, want nil for empty content", transcript)
	}
}

func TestNormalizeBackgroundTranscriptEventUserMessage(t *testing.T) {
	evt := &events.AgentEvent{
		Type:      events.UserMessage,
		Timestamp: time.Now(),
		Data: &events.UserMessageEvent{
			BaseEventData: events.BaseEventData{Timestamp: time.Now()},
			Content:       "please investigate the drop",
			Role:          "user",
		},
	}
	normalized, ok := NormalizeBackgroundTranscriptEvent(evt)
	if !ok {
		t.Fatal("expected ok=true for a non-empty user message")
	}
	if normalized.Type != backgroundTranscriptEventUserMessage {
		t.Fatalf("type = %q, want %q", normalized.Type, backgroundTranscriptEventUserMessage)
	}
	if normalized.Text != "please investigate the drop" {
		t.Fatalf("text = %q", normalized.Text)
	}
}

func TestNormalizeBackgroundTranscriptEventEmptyUserMessageIsSkipped(t *testing.T) {
	evt := &events.AgentEvent{
		Type: events.UserMessage,
		Data: &events.UserMessageEvent{Content: "   "},
	}
	if _, ok := NormalizeBackgroundTranscriptEvent(evt); ok {
		t.Fatal("expected ok=false for a blank user message")
	}
}

func TestNormalizeBackgroundTranscriptEventOrchestratorStartCarriesPrompt(t *testing.T) {
	evt := &events.AgentEvent{
		Type: OrchestratorAgentStart,
		Data: &OrchestratorAgentStartEvent{
			BaseEventData: events.BaseEventData{Timestamp: time.Now()},
			UserMessage:   "review the last three runs",
		},
	}
	normalized, ok := NormalizeBackgroundTranscriptEvent(evt)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if normalized.Role != "user" || normalized.Text != "review the last three runs" || normalized.SourceType != string(OrchestratorAgentStart) {
		t.Fatalf("normalized = %+v", normalized)
	}
}

func TestNormalizeBackgroundTranscriptEventOrchestratorEndCarriesResultOrError(t *testing.T) {
	okEvt := &events.AgentEvent{
		Type: OrchestratorAgentEnd,
		Data: &OrchestratorAgentEndEvent{
			BaseEventData: events.BaseEventData{Timestamp: time.Now()},
			Result:        "3 findings recorded",
			Success:       true,
		},
	}
	normalized, ok := NormalizeBackgroundTranscriptEvent(okEvt)
	if !ok || normalized.Text != "3 findings recorded" || normalized.Role != "assistant" || normalized.SourceType != string(OrchestratorAgentEnd) {
		t.Fatalf("success case: normalized=%+v ok=%v", normalized, ok)
	}

	failEvt := &events.AgentEvent{
		Type: OrchestratorAgentEnd,
		Data: &OrchestratorAgentEndEvent{
			BaseEventData: events.BaseEventData{Timestamp: time.Now()},
			Success:       false,
			Error:         "provider timeout",
		},
	}
	normalized, ok = NormalizeBackgroundTranscriptEvent(failEvt)
	if !ok || normalized.Text != "provider timeout" {
		t.Fatalf("failure case: normalized=%+v ok=%v", normalized, ok)
	}
}

func TestNormalizeBackgroundTranscriptEventToolCallStartAndEnd(t *testing.T) {
	startEvt := &events.AgentEvent{
		Type: events.ToolCallStart,
		Data: &events.ToolCallStartEvent{
			ToolName:   "execute_shell_command",
			ToolCallID: "tc-9",
			ToolParams: events.ToolParams{Arguments: `{"command":"ls"}`},
		},
	}
	normalized, ok := NormalizeBackgroundTranscriptEvent(startEvt)
	if !ok || normalized.Type != backgroundTranscriptEventToolCall {
		t.Fatalf("start: normalized=%+v ok=%v", normalized, ok)
	}
	if normalized.ToolCall == nil || normalized.ToolCall.Arguments != `{"command":"ls"}` || normalized.ToolCall.Status != "running" {
		t.Fatalf("start tool call = %+v", normalized.ToolCall)
	}

	endEvt := &events.AgentEvent{
		Type: events.ToolCallEnd,
		Data: &events.ToolCallEndEvent{
			ToolName:   "execute_shell_command",
			ToolCallID: "tc-9",
			Result:     "total 0",
			Duration:   150 * time.Millisecond,
		},
	}
	normalized, ok = NormalizeBackgroundTranscriptEvent(endEvt)
	if !ok || normalized.ToolCall == nil || normalized.ToolCall.Result != "total 0" || normalized.ToolCall.Status != "completed" {
		t.Fatalf("end: normalized=%+v ok=%v", normalized, ok)
	}

	errEvt := &events.AgentEvent{
		Type: events.ToolCallError,
		Data: &events.ToolCallErrorEvent{
			ToolName:   "execute_shell_command",
			ToolCallID: "tc-9",
			Error:      "permission denied",
		},
	}
	normalized, ok = NormalizeBackgroundTranscriptEvent(errEvt)
	if !ok || normalized.ToolCall == nil || normalized.ToolCall.Error != "permission denied" || normalized.ToolCall.Status != "failed" {
		t.Fatalf("error: normalized=%+v ok=%v", normalized, ok)
	}
}

func TestNormalizeBackgroundTranscriptEventIgnoresUnrelatedEventTypes(t *testing.T) {
	evt := &events.AgentEvent{
		Type: events.TokenUsage,
		Data: &events.TokenUsageEvent{ModelID: "test-model"},
	}
	if _, ok := NormalizeBackgroundTranscriptEvent(evt); ok {
		t.Fatal("expected ok=false for a token usage event — not part of the transcript contract")
	}
}
