package mcpagent

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestClaudeBridgeAllowedToolIdentifiersIncludesAdditional pins the fix for a
// real bug: enforced-mode Claude routing (MCPAGENT_CLAUDE_ENFORCE_HTTP_TOOL_ROUTING)
// hardcoded a 4-tool allowlist in two places (--allowedTools and the PreToolUse
// hook), silently rejecting any tool registered via withAdditionalBridgeTools.
// Both now derive from this single function.
func TestClaudeBridgeAllowedToolIdentifiersIncludesAdditional(t *testing.T) {
	got := claudeBridgeAllowedToolIdentifiers([]string{"my_custom_tool"}, nil)

	want := []string{
		"mcp__api-bridge__execute_shell_command",
		"mcp__api-bridge__diff_patch_workspace_file",
		"mcp__api-bridge__agent_browser",
		"mcp__api-bridge__get_api_spec",
		"mcp__api-bridge__my_custom_tool",
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %q in allowlist, got %v", w, got)
		}
	}

	// A caller's additional-tools list may legitimately re-name a core default
	// (see BuildBridgeMCPConfig's identical dedup rationale) — must not appear
	// twice.
	dup := claudeBridgeAllowedToolIdentifiers([]string{"agent_browser"}, nil)
	count := 0
	for _, g := range dup {
		if g == "mcp__api-bridge__agent_browser" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected agent_browser deduped to 1 occurrence, got %d in %v", count, dup)
	}
}

// TestClaudeHTTPRoutingHookAllowsAdditionalBridgeTool is the real proof: it
// generates the actual enforced-mode PreToolUse hook script (the same one
// installed into a live Claude Code session) and EXECUTES it with python3,
// feeding it a PreToolUse payload for a tool registered only via
// withAdditionalBridgeTools. Before the fix this was denied unconditionally
// (hardcoded 4-tool ALLOWED set); it must now be allowed (exit 0, no denial
// on stdout), while a tool that was never registered anywhere must still be
// denied.
func TestClaudeHTTPRoutingHookAllowsAdditionalBridgeTool(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 required to execute the generated hook script")
	}

	hookPath, err := writeClaudeHTTPRoutingHook([]string{"my_custom_tool"}, nil, []string{"WebSearch"})
	if err != nil {
		t.Fatalf("writeClaudeHTTPRoutingHook: %v", err)
	}

	// The hook's own contract: SystemExit(0) with EMPTY stdout means "allowed"
	// (Claude's default behavior applies); the deny path falls through, writes
	// a permissionDecision:"deny" JSON blob to stdout, and ALSO exits 0
	// naturally — so the decision lives in stdout content, never the exit code.
	runHook := func(toolName string) (denied bool, stdout string) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"tool_name": toolName})
		// #nosec G204 - hookPath is our own content-addressed path under
		// os.TempDir()/claude-code-hooks/, written by writeClaudeHTTPRoutingHook
		// moments earlier in this same test, not external/attacker input.
		cmd := exec.Command("python3", hookPath)
		cmd.Stdin = bytes.NewReader(payload)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("running hook for %q: %v", toolName, err)
		}
		return strings.Contains(out.String(), `"permissionDecision": "deny"`), out.String()
	}

	// The additional tool this agent explicitly registered must now be allowed.
	if denied, out := runHook("mcp__api-bridge__my_custom_tool"); denied {
		t.Errorf("additional bridge tool was denied: %s", out)
	}

	// A core tool must still be allowed (no regression).
	if denied, out := runHook("mcp__api-bridge__execute_shell_command"); denied {
		t.Errorf("core bridge tool was denied: %s", out)
	}

	// A tool that was never registered anywhere must still be denied — proves
	// this isn't just an accidental wildcard.
	if denied, out := runHook("mcp__api-bridge__totally_unregistered_tool"); !denied {
		t.Errorf("unregistered tool was NOT denied: %s", out)
	}
}

// TestClaudeHTTPRoutingHookPathIsContentAddressed pins the fix for a real
// concurrency bug: writeClaudeHTTPRoutingHook always wrote to the SAME fixed
// path regardless of the allowlist content. Before withAdditionalBridgeTools
// was wired into the allowlist (the fix this test file's first two tests
// pin), every agent wrote byte-identical content there, so the shared path
// was harmless. Once the content genuinely varies per agent, two concurrent
// agents with DIFFERENT registered tools would silently overwrite each
// other's allowlist file — whichever wrote last would govern BOTH sessions'
// enforcement. The path is now content-addressed: two DIFFERENT allowlists
// must never collide on the same file, and two agents with the SAME
// allowlist should safely share one (no needless growth, and no possibility
// of one clobbering the other since the bytes are identical either way).
func TestClaudeHTTPRoutingHookPathIsContentAddressed(t *testing.T) {
	pathA, err := writeClaudeHTTPRoutingHook([]string{"tool_a"}, nil, []string{"WebSearch"})
	if err != nil {
		t.Fatalf("writeClaudeHTTPRoutingHook(tool_a): %v", err)
	}
	pathB, err := writeClaudeHTTPRoutingHook([]string{"tool_b"}, nil, []string{"WebSearch"})
	if err != nil {
		t.Fatalf("writeClaudeHTTPRoutingHook(tool_b): %v", err)
	}
	if pathA == pathB {
		t.Fatalf("two DIFFERENT allowlists produced the SAME hook path — a concurrent agent race would silently overwrite the other's allowlist: %s", pathA)
	}

	pathA2, err := writeClaudeHTTPRoutingHook([]string{"tool_a"}, nil, []string{"WebSearch"})
	if err != nil {
		t.Fatalf("writeClaudeHTTPRoutingHook(tool_a) again: %v", err)
	}
	if pathA != pathA2 {
		t.Errorf("the SAME allowlist produced two different paths (expected content-addressed sharing): %s vs %s", pathA, pathA2)
	}
}
