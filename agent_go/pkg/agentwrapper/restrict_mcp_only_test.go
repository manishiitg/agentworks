package agent

import "testing"

// The server's fail-closed path turns any native tools mode into bridge-only.
func TestRestrictCodingAgentToolsToMCPOnly(t *testing.T) {
	for _, mode := range []string{"hybrid", "full", "full_unconfined"} {
		w := &LLMAgentWrapper{}
		w.runtime.Coding.AgentToolsMode = mode
		changed, err := w.RestrictCodingAgentToolsToMCPOnly()
		if err != nil || !changed || w.runtime.Coding.AgentToolsMode != "mcp_only" {
			t.Errorf("%s: changed=%v err=%v mode=%q", mode, changed, err, w.runtime.Coding.AgentToolsMode)
		}
	}
	for _, mode := range []string{"", "mcp_only"} {
		w := &LLMAgentWrapper{}
		w.runtime.Coding.AgentToolsMode = mode
		if changed, _ := w.RestrictCodingAgentToolsToMCPOnly(); changed {
			t.Errorf("%q: reported a change", mode)
		}
	}
	w := &LLMAgentWrapper{finalized: true}
	w.runtime.Coding.AgentToolsMode = "full"
	if _, err := w.RestrictCodingAgentToolsToMCPOnly(); err == nil {
		t.Error("a finalized agent must refuse the change")
	}
}
