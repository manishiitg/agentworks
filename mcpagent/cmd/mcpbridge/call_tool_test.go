package main

import "testing"

// call_tool must reach a discovered tool on exactly the route, deadline and
// session header a direct call uses, and refuse names that would leave it.
func TestCallToolTargetUsesDirectRoute(t *testing.T) {
	def := ToolDef{Name: "call_tool", Type: "call", CallTimeoutsMillis: map[string]int64{"dashboard": 660000}}

	target, args, err := callToolTarget(def, map[string]any{"tool_name": "dashboard", "arguments": map[string]any{"action": "list"}})
	if err != nil || target.Type != "custom" || target.Name != "dashboard" || target.TimeoutMillis == nil || *target.TimeoutMillis != 660000 || args["action"] != "list" {
		t.Fatalf("custom target = %+v args=%v err=%v", target, args, err)
	}
	target, args, err = callToolTarget(def, map[string]any{"tool_name": "create_issue", "server_name": "github", "arguments": `{"title":"x"}`})
	if err != nil || target.Type != "mcp" || target.Server != "github" || args["title"] != "x" {
		t.Fatalf("mcp target = %+v args=%v err=%v", target, args, err)
	}
	// A connected-app name finds its app without server_name (PLAT-856).
	target, _, err = callToolTarget(def, map[string]any{"tool_name": "notion__notion-fetch"})
	if err != nil || target.Type != "mcp" || target.Server != "notion" || target.Name != "notion__notion-fetch" {
		t.Fatalf("app target = %+v err=%v", target, err)
	}
	for _, bad := range []map[string]any{
		{},
		{"tool_name": "call_tool"},
		{"tool_name": "../virtual/get_api_spec"},
		{"tool_name": ".."},
		{"tool_name": "dashboard", "server_name": ".."},
		{"tool_name": "dashboard", "server_name": "a/b"},
		{"tool_name": "dashboard", "arguments": []any{1}},
	} {
		if _, _, err := callToolTarget(def, bad); err == nil {
			t.Fatalf("callToolTarget(%v) accepted", bad)
		}
	}
}
