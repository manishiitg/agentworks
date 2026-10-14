package agents

import (
	"testing"

	mcpagent "github.com/manishiitg/mcpagent/agent"
)

func TestPlatformBridgeToolsIncludesReadImageOnlyWhenRegistered(t *testing.T) {
	if got := platformBridgeToolsFromDirectTools([]mcpagent.ToolDefinition{{Name: "send_email"}}); len(got) != 0 {
		t.Fatalf("platform bridge tools without read_image = %v, want none", got)
	}

	got := platformBridgeToolsFromDirectTools([]mcpagent.ToolDefinition{
		{Name: "send_email"},
		{Name: "read_image"},
	})
	if len(got) != 1 || got[0] != "read_image" {
		t.Fatalf("platform bridge tools = %v, want [read_image]", got)
	}
}
