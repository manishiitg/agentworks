package agentworksclient

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// The chat plugin shows one tool; each old capability is an action, and a call that names no action, an unknown
// one, or leaves out a needed argument is refused in plain words.
func TestPluginExposesOneToolWithActions(t *testing.T) {
	s, err := NewMusePluginMCPServer(context.Background(), t.TempDir(), "host", func() (string, error) { return "s1", nil })
	if err != nil {
		t.Fatal(err)
	}
	tools := s.ListTools()
	if len(tools) != 1 || tools[PluginToolName] == nil {
		t.Fatalf("want exactly the %q tool, got %d tools", PluginToolName, len(tools))
	}
	tool := tools[PluginToolName]
	for _, action := range []string{"send", "ack", "reply", "read", "peers", "set_purpose", "get_file", "ping_person"} {
		if !strings.Contains(tool.Tool.Description, "- "+action) {
			t.Errorf("action %s is missing from the tool description", action)
		}
	}
	call := func(args map[string]any) *mcp.CallToolResult {
		out, e := tool.Handler(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}})
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	text := func(r *mcp.CallToolResult) string { return r.Content[0].(mcp.TextContent).Text }
	if r := call(map[string]any{}); !r.IsError || !strings.Contains(text(r), "action must be one of") {
		t.Fatalf("no action: %+v", r)
	}
	if r := call(map[string]any{"action": "nope"}); !r.IsError || !strings.Contains(text(r), "action must be one of") {
		t.Fatalf("unknown action: %+v", r)
	}
	if r := call(map[string]any{"action": "send", "target": "agent:x"}); !r.IsError || !strings.Contains(text(r), "action=send needs: message") {
		t.Fatalf("missing argument: %+v", r)
	}
}
