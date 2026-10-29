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
	runtime.Tools.AdditionalBridge = []string{"request_clarification", "read_skill", "schedules"}
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
}
