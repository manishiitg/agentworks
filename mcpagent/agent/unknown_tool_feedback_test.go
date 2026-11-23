package mcpagent

import (
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func toolFixture(name string) llmtypes.Tool {
	return llmtypes.Tool{
		Type:     "function",
		Function: &llmtypes.FunctionDefinition{Name: name},
	}
}

// The correction used to be a literal always naming "get_prompt, get_resource
// (virtual tools)". Both are registered only when a connected MCP server
// advertises prompts or resources, so for every NoServers agent — every Pulse
// reviewer, fixer, and background agent — it named tools that were not there.
// The model acts on recovery text, so a wrong hint costs another failed call.
func TestUnknownToolFeedbackNamesOnlyThisAgentsTools(t *testing.T) {
	agent := &Agent{
		filteredTools: []llmtypes.Tool{
			toolFixture("get_pulse_module_state"),
			toolFixture("start_pulse_fix_attempt"),
		},
	}

	msg := agent.unknownToolFeedback("get_resource")

	if strings.Contains(msg, "get_resource (virtual tools)") || strings.Contains(msg, "get_prompt") {
		t.Fatalf("still advertises unregistered virtual tools:\n%s", msg)
	}
	for _, want := range []string{"get_pulse_module_state", "start_pulse_fix_attempt"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("missing a tool this agent actually has (%s):\n%s", want, msg)
		}
	}
}

// A denied tool must not be offered as the way out of a failed call.
func TestUnknownToolFeedbackRespectsTheAllowList(t *testing.T) {
	agent := &Agent{
		filteredTools: []llmtypes.Tool{
			toolFixture("get_pulse_module_state"),
			toolFixture("mark_pulse_final_command_result"),
		},
		toolAllowList: map[string]bool{"get_pulse_module_state": true},
	}

	msg := agent.unknownToolFeedback("nope")

	if strings.Contains(msg, "mark_pulse_final_command_result") {
		t.Fatalf("offered a tool excluded from this session:\n%s", msg)
	}
}

// An agent with nothing callable should be told to stop, not handed an empty
// list it will read as "try again".
func TestUnknownToolFeedbackTellsAToollessAgentToStop(t *testing.T) {
	agent := &Agent{}

	msg := agent.unknownToolFeedback("anything")

	if !strings.Contains(msg, "no callable tools") {
		t.Fatalf("did not state that the agent has no tools:\n%s", msg)
	}
	if !strings.Contains(msg, "Do not retry") {
		t.Fatalf("did not tell the agent to stop retrying:\n%s", msg)
	}
}
