package agent

import "testing"

// Full CLI is chosen before finalization and preserves the native-tools-off
// choice; the retired hybrid and full_unconfined names become full (it only
// applies inside the host's lock).
func TestAgyFullCLIUpgrade(t *testing.T) {
	for mode, want := range map[string]string{"mcp_only": "mcp_only", "hybrid": "full", "full_unconfined": "full", "full": "full"} {
		w := &LLMAgentWrapper{config: LLMAgentConfig{Provider: "agy-cli"}}
		w.runtime = runtimeConfigForLLMAgent(LLMAgentConfig{Provider: "agy-cli", CodingAgentToolsMode: mode}, nil, nil, "", nil)
		changed, err := w.UpgradeCodingAgentToolsToFull()
		if err != nil || changed != (want == "full") || w.runtime.Coding.AgentToolsMode != want {
			t.Fatalf("mode %s: changed=%v runtime=%s err=%v", mode, changed, w.runtime.Coding.AgentToolsMode, err)
		}
		w.finalized = true
		if _, err := w.UpgradeCodingAgentToolsToFull(); err == nil {
			t.Fatal("finalized AGY session changed mode")
		}
	}
}
