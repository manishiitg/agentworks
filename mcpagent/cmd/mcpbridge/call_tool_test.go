package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

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
	// Virtual tools route to /tools/virtual/{name} so X-Virtual-Scope-ID is forwarded.
	for _, vt := range []string{"search_tools", "get_api_spec"} {
		target, _, err = callToolTarget(def, map[string]any{"tool_name": vt})
		if err != nil || target.Type != "virtual" || target.Name != vt {
			t.Fatalf("virtual target(%s) = %+v err=%v", vt, target, err)
		}
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

func TestReadBridgeResponseBodyEnforcesLimit(t *testing.T) {
	body, err := readBridgeResponseBody(bytes.NewReader([]byte("hello")), 5)
	if err != nil || string(body) != "hello" {
		t.Fatalf("exact limit failed: body=%q err=%v", string(body), err)
	}
	if _, err := readBridgeResponseBody(bytes.NewReader([]byte("hello!")), 5); err == nil {
		t.Fatal("expected error when bridge response exceeds limit")
	}
}

// #4560: live call_tool timeouts load from the chat-stable timeouts file instead
// of changing MCP_TOOLS_FILE when custom HTTP tools toggle between turns.
func TestLoadBridgeCallTimeoutsReadsChatStableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".mcpbridge-call-timeouts-test.json")
	if err := os.WriteFile(path, []byte(`{"execute_local_shell_command":300000}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := loadBridgeCallTimeouts(path)
	if got["execute_local_shell_command"] != 300000 {
		t.Fatalf("loaded timeouts = %v", got)
	}
	def := ToolDef{Name: "call_tool", Type: "call", CallTimeoutsMillis: got}
	target, _, err := callToolTarget(def, map[string]any{"tool_name": "execute_local_shell_command"})
	if err != nil || target.TimeoutMillis == nil || *target.TimeoutMillis != 300000 {
		t.Fatalf("callToolTarget = %+v err=%v", target, err)
	}
}
