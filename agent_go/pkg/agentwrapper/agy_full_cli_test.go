package agent

import "testing"

// Local Full CLI must preserve the native-tools-off choice and be selected
// before finalization; the server's single-user gate is checked separately.
func TestAgyLocalFullCLIUpgrade(t *testing.T) {
	for _, mode := range []string{"mcp_only", "hybrid", "full_unconfined"} {
		w := &LLMAgentWrapper{config: LLMAgentConfig{Provider: "agy-cli"}}
		w.runtime = runtimeConfigForLLMAgent(LLMAgentConfig{Provider: "agy-cli", CodingAgentToolsMode: mode}, nil, nil, "", nil)
		changed, err := w.UpgradeCodingAgentToolsToFullUnconfined()
		want := mode
		if mode == "hybrid" {
			want = "full_unconfined"
		}
		if err != nil || changed != (mode == "hybrid") || w.runtime.Coding.AgentToolsMode != want {
			t.Fatalf("mode %s: changed=%v runtime=%s err=%v", mode, changed, w.runtime.Coding.AgentToolsMode, err)
		}
		w.finalized = true
		if _, err := w.UpgradeCodingAgentToolsToFullUnconfined(); err == nil {
			t.Fatal("finalized AGY session changed mode")
		}
	}
}
