package agent

import "testing"

func TestRuntimeConfigProjectsAdditionalBridgeTools(t *testing.T) {
	config := LLMAgentConfig{
		AdditionalBridgeTools: []string{"read_skill"},
	}

	runtime := runtimeConfigForLLMAgent(config, nil, nil, "", nil)
	if len(runtime.Tools.AdditionalBridge) != 1 || runtime.Tools.AdditionalBridge[0] != "read_skill" {
		t.Fatalf("additional bridge tools = %v, want [read_skill]", runtime.Tools.AdditionalBridge)
	}
}
