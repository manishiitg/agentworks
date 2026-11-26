package mcpagent

import (
	"context"
	"testing"
)

func TestRetiredResourceAndPromptToolsRejectExecution(t *testing.T) {
	a := &Agent{}
	for _, name := range []string{"get_resource", "get_prompt"} {
		if _, err := a.handleVirtualTool(context.Background(), name, map[string]interface{}{"server": "demo", "uri": "legacy://data", "name": "guide"}); err == nil {
			t.Fatalf("retired tool %s still executes", name)
		}
	}
}
