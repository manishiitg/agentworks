package mcpagent

import (
	"strings"
	"testing"
)

// A profile that removes a core bridge tool must remove it from EVERY surface
// derived from the catalog. These were computed independently, so a removed
// tool could still be named in the routing prompt and permitted by Claude's
// allowlist -- telling the model to call something the bridge never registered,
// which is the "tool not registered for session" failure the admission filter
// exists to prevent.
func TestExcludedBridgeToolLeavesEverySurface(t *testing.T) {
	excludeShell := &Agent{bridgeToolAdmit: func(name string) bool { return name != "execute_shell_command" }}

	prompt := bridgeRoutingExplicitInstructions(excludeShell.admitsCoreBridgeTool)
	if strings.Contains(prompt, "execute_shell_command") {
		t.Fatalf("routing prompt still instructs the model to call an excluded tool:\n%s", prompt)
	}
	if !strings.Contains(prompt, "diff_patch_workspace_file") {
		t.Fatalf("routing prompt dropped a tool that was NOT excluded:\n%s", prompt)
	}

	allowed := strings.Join(claudeBridgeAllowedToolIdentifiers(nil, excludeShell.admitsBridgeTool), ",")
	if strings.Contains(allowed, "mcp__api-bridge__execute_shell_command") {
		t.Fatalf("Claude allowlist still permits an excluded tool: %s", allowed)
	}
	if !strings.Contains(allowed, "mcp__api-bridge__diff_patch_workspace_file") {
		t.Fatalf("Claude allowlist dropped a tool that was NOT excluded: %s", allowed)
	}
	// get_api_spec is virtual: it is the discovery door and must survive any
	// profile policy, or the agent loses the ability to find anything at all.
	if !strings.Contains(allowed, "mcp__api-bridge__get_api_spec") {
		t.Fatalf("virtual discovery tool was filtered out: %s", allowed)
	}
}

// Guidance that is only reachable THROUGH the shell tool (the curl forms, the
// blocking human_feedback protocol) is false once the shell tool is gone, so
// it must not survive on the strength of naming a different tool.
func TestRoutingPromptDropsShellDependentGuidanceWithoutShell(t *testing.T) {
	excludeShell := &Agent{bridgeToolAdmit: func(name string) bool { return name != "execute_shell_command" }}
	prompt := bridgeRoutingExplicitInstructions(excludeShell.admitsCoreBridgeTool)
	for _, forbidden := range []string{"curl", "MCP_AUTH", "human_feedback"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prompt kept %q guidance that requires the excluded shell tool:\n%s", forbidden, prompt)
		}
	}
}

// Even a policy that rejects everything cannot remove get_api_spec: it is
// virtual, and it is the discovery door. So the block never disappears -- it
// narrows to discovery only, and must still name no excluded tool.
func TestRoutingPromptNarrowsToDiscoveryWhenPolicyRejectsEverything(t *testing.T) {
	excludeAll := &Agent{bridgeToolAdmit: func(string) bool { return false }}
	prompt := bridgeRoutingExplicitInstructions(excludeAll.admitsCoreBridgeTool)
	if !strings.Contains(prompt, "get_api_spec") {
		t.Fatalf("discovery tool must survive any policy:\n%s", prompt)
	}
	for _, forbidden := range []string{"execute_shell_command", "diff_patch_workspace_file", "curl"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prompt still names excluded %q:\n%s", forbidden, prompt)
		}
	}
}

// The default (no profile policy) must be unchanged -- this filter should be
// invisible to every existing caller.
func TestRoutingSurfacesAreUnchangedWithoutAPolicy(t *testing.T) {
	none := &Agent{}
	prompt := bridgeRoutingExplicitInstructions(none.admitsCoreBridgeTool)
	for _, want := range []string{"execute_shell_command", "diff_patch_workspace_file", "get_api_spec", "search_tools"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("unpoliced prompt is missing %q:\n%s", want, prompt)
		}
	}
	allowed := strings.Join(claudeBridgeAllowedToolIdentifiers(nil, none.admitsBridgeTool), ",")
	for _, want := range []string{"execute_shell_command", "diff_patch_workspace_file", "agent_browser", "get_api_spec"} {
		if !strings.Contains(allowed, "mcp__api-bridge__"+want) {
			t.Fatalf("unpoliced allowlist is missing %q: %s", want, allowed)
		}
	}
}
