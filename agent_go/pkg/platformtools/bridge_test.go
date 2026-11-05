package platformtools

import (
	"slices"
	"testing"

	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// A live Crew chat (29 Oct) sent all 27 admitted platform tools,
// about 26K tokens, with every model call. Only the core working tools and
// the caller's explicit bridge tools are native; the rest stay discoverable.
func TestBridgeRuntimeKeepsOnlyCoreAndExplicitTools(t *testing.T) {
	projected := []mcpagent.ToolDefinition{}
	for _, name := range []string{"terminal", "browser", "files", "needs_you", "workspace", "dashboard", "vault", "brain", "database"} {
		projected = append(projected, mcpagent.ToolDefinition{Name: name})
	}
	runtime := mcpagent.RuntimeConfig{}
	runtime.Tools.AdditionalBridge = []string{"needs_you", "read_skill", "automations"}
	got := BridgeRuntime(projected, runtime)

	want := []string{"terminal", "browser", "files", "needs_you", "read_skill"}
	if !slices.Equal(got.Tools.AdditionalBridge, want) {
		t.Fatalf("bridge = %v, want %v", got.Tools.AdditionalBridge, want)
	}
	admit := got.Coding.BridgeToolAdmit
	if admit("vault") || admit("execute_shell_command") || !admit("terminal") {
		t.Fatalf("admission must follow the bridge: vault=%v execute_shell_command=%v terminal=%v",
			admit("vault"), admit("execute_shell_command"), admit("terminal"))
	}

	// Full CLI (owner, 5 Nov: "files should be removed for sure in full cli"): an RTS Full CLI chat made
	// 1,374 model calls and no direct files call, while the tool cost about 7.8 KB per call. files leaves the
	// bridge, but stays in the projected catalog that search_tools and call_tool serve.
	full := BridgeRuntime(projected, runtime, "files")
	if slices.Contains(full.Tools.AdditionalBridge, "files") || full.Coding.BridgeToolAdmit("files") {
		t.Fatalf("Full CLI bridge still offers files: %v", full.Tools.AdditionalBridge)
	}
	if !slices.ContainsFunc(projected, func(tool mcpagent.ToolDefinition) bool { return tool.Name == "files" }) || !full.Coding.BridgeToolAdmit("terminal") {
		t.Fatal("Full CLI must keep files in the catalog and the other core tools on the bridge")
	}
}
