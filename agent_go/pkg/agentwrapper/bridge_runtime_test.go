package agent

import "testing"

func TestRuntimeConfigPassesResolvedBridgeToCodingProviders(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/release/bin/mcpbridge")
	runtime := runtimeConfigForLLMAgent(LLMAgentConfig{}, nil, nil, "", nil)
	if runtime.Coding.BridgeBinary != "/release/bin/mcpbridge" {
		t.Fatalf("bridge path lost: %q", runtime.Coding.BridgeBinary)
	}
}
