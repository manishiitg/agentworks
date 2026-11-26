package mcpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	llm "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func discoveryResult(t *testing.T, a *Agent, ctx context.Context, args map[string]interface{}) string {
	t.Helper()
	raw, err := a.handleVirtualTool(ctx, "search_tools", args)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(raw)) {
		t.Fatalf("invalid result: %s", raw)
	}
	return raw
}

func TestDiscoveryUsesLiveSessionAndTurnPermissions(t *testing.T) {
	a := codeExecutionPromptAgent()
	addDirectToolFixture(t, a, directToolFixture("read_record", "records"))
	addDirectToolFixture(t, a, directToolFixture("delete_record", "private_admin"))
	a.setToolAccess([]string{"read_record"})
	first := discoveryResult(t, a, context.Background(), nil)
	if !strings.Contains(first, "read_record") || strings.Contains(first, "delete_record") || strings.Contains(first, "private_admin") {
		t.Fatalf("permission leak: %s", first)
	}
	// Tools and access change after prompt construction. No cached inventory may
	// determine what this discovery request sees.
	addDirectToolFixture(t, a, directToolFixture("new_record", "records"))
	a.setToolAccess([]string{"new_record"})
	second := discoveryResult(t, a, context.Background(), nil)
	if !strings.Contains(second, "new_record") || strings.Contains(second, "read_record") {
		t.Fatalf("stale discovery: %s", second)
	}
	ctx := context.WithValue(context.Background(), turnPolicyContextKey{}, normalizedToolPolicy{allowed: map[string]bool{"read_record": true}})
	third := discoveryResult(t, a, ctx, nil)
	if !strings.Contains(third, "read_record") || strings.Contains(third, "new_record") {
		t.Fatalf("turn policy ignored: %s", third)
	}
	b := codeExecutionPromptAgent()
	addDirectToolFixture(t, b, directToolFixture("other_session", "other"))
	if strings.Contains(discoveryResult(t, a, ctx, nil), "other_session") {
		t.Fatal("cross-session registry leak")
	}
	if strings.Contains(a.outgoingSystemPrompt(), "new_record") {
		t.Fatal("dynamic catalog was still inlined")
	}
}

func TestDiscoveryFindsIntentAndProvidesEnumerationFallback(t *testing.T) {
	a := codeExecutionPromptAgent()
	tool := directToolFixture("opaque_7319", "automations")
	tool.Definition.Function.Description = "Create a recurring project chat message with a timezone."
	addDirectToolFixture(t, a, tool)
	matched := discoveryResult(t, a, context.Background(), map[string]interface{}{"query": "recurring message"})
	if !strings.Contains(matched, "opaque_7319") {
		t.Fatalf("description was not searched: %s", matched)
	}
	miss := discoveryResult(t, a, context.Background(), map[string]interface{}{"query": "unmatchedword"})
	if !strings.Contains(miss, "no_matches") || !strings.Contains(miss, "automations") || strings.Contains(miss, "opaque_7319") {
		t.Fatalf("missing safe fallback: %s", miss)
	}
	page := discoveryResult(t, a, context.Background(), map[string]interface{}{"group": "automations"})
	if !strings.Contains(page, "opaque_7319") {
		t.Fatal("enumeration lost tool")
	}
	spec, err := a.handleGetAPISpec(context.Background(), map[string]interface{}{"tool_name": "opaque_7319"})
	if err != nil || !strings.Contains(spec, "opaque_7319") {
		t.Fatalf("discovered name cannot resolve schema: %v", err)
	}
	a.setToolAccess([]string{"some_other_tool"})
	if _, err := a.handleGetAPISpec(context.Background(), map[string]interface{}{"tool_name": "opaque_7319"}); err == nil {
		t.Fatal("cached schema bypassed revoked permission")
	}
}

func TestDiscoveryFiltersMCPServersAndPaginates(t *testing.T) {
	a := codeExecutionPromptAgent()
	a.toolFilter = NewToolFilter(nil, []string{"selected-server"}, nil, nil, nil)
	for _, server := range []string{"selected-server", "hidden-server"} {
		for i := 0; i < 12; i++ {
			tool := directToolFixture(fmt.Sprintf("%s_%d", server, i), "")
			tool.Kind, tool.Source = toolImplementationMCP, server
			addDirectToolFixture(t, a, tool)
		}
	}
	first := discoveryResult(t, a, context.Background(), map[string]interface{}{"server_name": "selected_server", "limit": float64(2)})
	if strings.Contains(first, "hidden") || !strings.Contains(first, `"next_offset":10`) {
		t.Fatalf("server filtering/page failed: %s", first)
	}
	last := discoveryResult(t, a, context.Background(), map[string]interface{}{"offset": 10, "limit": 2})
	if strings.Contains(last, "next_offset") || !strings.Contains(last, "selected-server_9") {
		t.Fatalf("last page failed: %s", last)
	}
	// Agents asked for limit 30 and lost a turn to an error (PLAT-825); a large page is capped, not refused.
	if _, err := a.handleSearchTools(context.Background(), map[string]interface{}{"limit": float64(30)}); err != nil {
		t.Fatalf("limit 30 refused: %v", err)
	}
	for _, args := range []map[string]interface{}{{"offset": -1}, {"limit": 1.5}, {"query": 1}, {"group": strings.Repeat("x", 513)}} {
		if _, err := a.handleSearchTools(context.Background(), args); err == nil {
			t.Fatalf("accepted invalid input: %#v", args)
		}
	}
}

func TestDynamicRuntimePreservesCallerInstructionsAndSkills(t *testing.T) {
	a := codeExecutionPromptAgent()
	a.provider, a.modelID = llm.ProviderAgyCLI, "gemini-3.8-flash-high"
	a.setInstructions("PRODUCT POLICY")
	a.appendInstructions("CALLER DYNAMIC INSTRUCTION")
	mustAttachSkill(t, a, &llmtypes.Skill{Name: "fresh-skill", Description: "Handle a fresh capability", Content: "FRESH BODY"})
	ctx := context.WithValue(context.Background(), turnPolicyContextKey{}, normalizedToolPolicy{allowed: map[string]bool{"diff_patch_workspace_file": true}})
	got := a.outgoingSystemPromptForContext(ctx)
	for _, want := range []string{"PRODUCT POLICY", "CALLER DYNAMIC INSTRUCTION", "fresh-skill", "search_tools", "get_api_spec"} {
		if !strings.Contains(got, want) {
			t.Fatalf("lost %q: %s", want, got)
		}
	}
	for _, absent := range []string{"FRESH BODY", "MCP_AUTH", "execute_shell_command"} {
		if strings.Contains(got, absent) {
			t.Fatalf("inlined skill body or unavailable route %q", absent)
		}
	}
	a.setInstructions(got)
	if current := a.outgoingSystemPromptForContext(ctx); strings.Count(current, "<runtime_tools>") != 1 || strings.Count(current, "<available_tools>") != 0 {
		t.Fatal("recomposition duplicated runtime/discovery sections")
	}
	// Replacing an attached skill updates its discovery description and body.
	mustAttachSkill(t, a, &llmtypes.Skill{Name: "fresh-skill", Description: "Updated capability", Content: "UPDATED BODY"})
	if !strings.Contains(a.outgoingSystemPromptForContext(ctx), "Updated capability") {
		t.Fatal("stale skill description")
	}
	read, err := a.readOneAttachedSkill("fresh-skill", "")
	if err != nil || read.Content != "UPDATED BODY" {
		t.Fatalf("stale skill body: %#v %v", read, err)
	}
}

func TestDiscoverySurvivesCoreBridgePolicyAndPreservesNativeAPIMode(t *testing.T) {
	a := &Agent{bridgeToolAdmit: func(string) bool { return false }}
	def := a.lookupBridgeTool("search_tools", "virtual", nil)
	if def == nil || def.Name != "search_tools" {
		t.Fatal("bridge lost discovery under a restrictive profile")
	}
	a.setInstructions("API PRODUCT POLICY")
	if a.instructions() != "API PRODUCT POLICY" {
		t.Fatal("native API mode gained HTTP/discovery prompt")
	}
}

func TestHTTPDiscoveryEnforcesPolicyWithoutInProcessTurnContext(t *testing.T) {
	logger := loggerv2.NewDefault()
	codeexec.InitRegistry(nil, nil, nil, logger)
	a := codeExecutionPromptAgent()
	a.sessionID = "discovery-http-" + t.Name()
	t.Cleanup(func() { codeexec.CleanupSession(a.sessionID) })
	addDirectToolFixture(t, a, directToolFixture("http_read", "records"))
	addDirectToolFixture(t, a, directToolFixture("http_mutate", "private_mutations"))
	codeexec.SetSessionToolAllowList(a.sessionID, map[string]bool{"http_read": true})
	codeexec.InitRegistryVirtualToolsForSession(a.virtualToolScopeID(), map[string]func(context.Context, map[string]interface{}) (string, error){"search_tools": a.handleSearchTools}, logger)
	// This is the bridge HTTP path: a background request, not Session.Run's ctx.
	raw, err := codeexec.CallVirtualToolWithSession(context.Background(), a.virtualToolScopeID(), "search_tools", nil)
	if err != nil || !strings.Contains(raw, "http_read") || strings.Contains(raw, "http_mutate") || strings.Contains(raw, "private_mutations") {
		t.Fatalf("HTTP discovery bypassed turn policy: %s %v", raw, err)
	}
	if _, err := a.handleGetAPISpec(context.Background(), map[string]interface{}{"tool_name": "http_read"}); err != nil {
		t.Fatal(err)
	}
	codeexec.SetSessionToolAllowList(a.sessionID, map[string]bool{"http_mutate": true, "missing_tool": true})
	if _, err := a.handleGetAPISpec(context.Background(), map[string]interface{}{"tool_name": "http_read"}); err == nil {
		t.Fatal("HTTP metadata schema cache bypassed permission revocation")
	}
	_, err = a.handleGetAPISpec(context.Background(), map[string]interface{}{"tool_name": "missing_tool"})
	if err == nil || strings.Contains(err.Error(), "http_read") {
		t.Fatalf("HTTP schema error leaked a denied alternative: %v", err)
	}
	raw = discoveryResult(t, a, context.Background(), nil)
	if !strings.Contains(raw, "http_mutate") || strings.Contains(raw, "http_read") {
		t.Fatalf("HTTP discovery did not refresh: %s", raw)
	}
}

// Compare the same 70-custom/44-MCP shape as the saved Code prompt, without
// using private names, descriptions, grants, or credentials from that session.
func TestDiscoveryPromptBudgetWithFixedToolFixture(t *testing.T) {
	a := codeExecutionPromptAgent()
	a.setInstructions("PRODUCT IDENTITY AND ACCESS POLICY")
	before := a.outgoingSystemPrompt()
	for i := 0; i < 70; i++ {
		addDirectToolFixture(t, a, directToolFixture(fmt.Sprintf("fixture_custom_%02d", i), fmt.Sprintf("group_%02d", i%22)))
	}
	for i := 0; i < 44; i++ {
		tool := directToolFixture(fmt.Sprintf("fixture_mcp_%02d", i), "")
		tool.Kind, tool.Source = toolImplementationMCP, "fixture-server"
		addDirectToolFixture(t, a, tool)
	}
	discovery := a.outgoingSystemPrompt()
	if discovery != before || strings.Contains(discovery, "fixture_custom_") || strings.Contains(discovery, "fixture_mcp_") {
		t.Fatal("registering 114 tools expanded the system prompt")
	}
	t.Logf("114 tools add no catalog bytes; discovery prompt=%d bytes", len(discovery))
}
