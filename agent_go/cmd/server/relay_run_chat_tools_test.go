package server

import (
	"slices"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
)

// The Run chat's tool surface is declared twice (the product manifest the gate enforces, and the tools the server
// registers). They must be the same four tools: a new Relay tool is refused in Run mode until someone adds it to both.
func TestRelayRunChatToolSurfaceIsExact(t *testing.T) {
	declared, err := relayproduct.RunTools()
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(declared)
	want := slices.Clone(relayRunChatTools)
	slices.Sort(want)
	if !slices.Equal(declared, want) {
		t.Fatalf("Run chat tools: manifest %v, server %v", declared, want)
	}
	for _, forbidden := range []string{"execute_shell_command", "test_relay", "publish_relay", "set_workflow_secret", "call_mcp_tool", "update_workflow_config"} {
		if slices.Contains(declared, forbidden) {
			t.Fatalf("%s must not be a Run mode tool", forbidden)
		}
	}
}
