package mcpagent

import (
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/events"
)

// The fixture is the REAL defect observed live: codex over tmux emitted two
// ToolCallStart events and zero ToolCallEnd, leaving the product's tool chips
// spinning forever. Every pre-existing assertion passed on exactly this data.
func TestToolLifecycleCatchesUnpairedStart(t *testing.T) {
	evs := []*events.AgentEvent{
		{Data: &events.ToolCallStartEvent{ToolCallID: "call_7KhK", ToolName: "exec"}},
		{Data: &events.ToolCallStartEvent{ToolCallID: "call_8lQg", ToolName: "exec"}},
	}
	tl := collectToolLifecycle(evs)

	// The OLD check — count of starts — is satisfied by this data, which is
	// exactly why the bug shipped. Assert that explicitly so this test also
	// documents why counting starts is insufficient.
	if len(tl.starts) == 0 {
		t.Fatal("precondition: the old toolChunks>0 check was supposed to pass here")
	}

	err := toolLifecycleError(tl)
	if err == nil {
		t.Fatal("toolLifecycleError returned nil for 2 starts / 0 ends")
	}
	if !strings.Contains(err.Error(), "never ended") {
		t.Fatalf("error should name the unfinished calls, got: %v", err)
	}
}

func TestToolLifecyclePassesWhenPaired(t *testing.T) {
	evs := []*events.AgentEvent{
		{Data: &events.ToolCallStartEvent{ToolCallID: "a", ToolName: "execute_shell_command"}},
		{Data: &events.ToolCallEndEvent{ToolCallID: "a", ToolName: "execute_shell_command"}},
	}
	if err := toolLifecycleError(collectToolLifecycle(evs)); err != nil {
		t.Fatalf("paired start/end should pass, got: %v", err)
	}
}

func TestToolLifecycleCatchesNameDrift(t *testing.T) {
	evs := []*events.AgentEvent{
		{Data: &events.ToolCallStartEvent{ToolCallID: "a", ToolName: "execute_shell_command"}},
		{Data: &events.ToolCallEndEvent{ToolCallID: "a", ToolName: "something_else"}},
	}
	if err := toolLifecycleError(collectToolLifecycle(evs)); err == nil {
		t.Fatal("a tool whose name changes between start and end should fail")
	}
}

func TestTurnTokenUsageAssertions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		usage   turnTokenUsage
		wantErr bool
	}{
		{"no usage events at all", turnTokenUsage{}, true},
		{"zero input", turnTokenUsage{Events: 1, Prompt: 0, Completion: 5, Total: 5}, true},
		{"zero output", turnTokenUsage{Events: 1, Prompt: 5, Completion: 0, Total: 5}, true},
		{"total does not add up", turnTokenUsage{Events: 1, Prompt: 100, Completion: 20, Total: 50}, true},
		{"valid", turnTokenUsage{Events: 1, Prompt: 100, Completion: 20, Total: 120, Cache: 64}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := turnTokenUsageError(tc.usage)
			if tc.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}
