package mcpagent

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func customToolFixture(name, category string) registeredTool {
	return directToolFixture(name, category)
}

// The bridge prompt teaches `$MCP_CUSTOM/{tool_name}` and states the categories
// are "not MCP servers … with no category segment", so an agent that has only
// addressed built-in tools that way passes server_name="custom". That used to
// fail with "custom is not available", listing the very categories the prompt
// told it not to use — and the error path filters "custom" out of that list, so
// the name the agent was taught never appears. A standalone Pulse Fixer lost a
// call to this on 2026-08-01 requesting four workflow tools.
func TestGetAPISpecResolvesToolsWhenServerNameIsCustom(t *testing.T) {
	agent := &Agent{
		toolRegistry: directToolRegistry(
			customToolFixture("start_pulse_fix_attempt", "workflow"),
			customToolFixture("mark_pulse_module_result", "workflow"),
			customToolFixture("get_pulse_module_state", "workflow"),
			customToolFixture("get_pulse_finding_backlog", "workflow"),
		),
		toolFilter: NewToolFilter(nil, nil, nil, nil, nil),
	}

	spec, err := agent.handleGetAPISpec(context.Background(), map[string]interface{}{
		"server_name": "custom",
		"tool_name": []interface{}{
			"start_pulse_fix_attempt", "mark_pulse_module_result",
			"get_pulse_module_state", "get_pulse_finding_backlog",
		},
	})
	if err != nil {
		t.Fatalf("get_api_spec(server_name=custom) failed: %v", err)
	}
	for _, want := range []string{"start_pulse_fix_attempt", "get_pulse_finding_backlog"} {
		if !strings.Contains(spec, want) {
			t.Fatalf("spec is missing %q:\n%s", want, spec)
		}
	}
}

// $MCP_CUSTOM is category-free, so one request may legitimately span
// categories. Splitting it would expose internal grouping the prompt told the
// agent to ignore.
func TestGetAPISpecCustomServerSpansCategories(t *testing.T) {
	agent := &Agent{
		toolRegistry: directToolRegistry(
			customToolFixture("get_pulse_module_state", "workflow"),
			customToolFixture("notify_user", "human_tools"),
		),
		toolFilter: NewToolFilter(nil, nil, nil, nil, nil),
	}

	spec, err := agent.handleGetAPISpec(context.Background(), map[string]interface{}{
		"server_name": "custom",
		"tool_name":   []interface{}{"get_pulse_module_state", "notify_user"},
	})
	if err != nil {
		t.Fatalf("cross-category custom request failed: %v", err)
	}
	if !strings.Contains(spec, "get_pulse_module_state") || !strings.Contains(spec, "notify_user") {
		t.Fatalf("spec dropped a category:\n%s", spec)
	}
}

// Resolution keys off tool names, which are unique across custom tools, so an
// unknown tool must still surface an error rather than being quietly absorbed.
func TestGetAPISpecUnknownToolStillErrors(t *testing.T) {
	agent := &Agent{
		toolRegistry: directToolRegistry(customToolFixture("get_pulse_module_state", "workflow")),
		toolFilter:   NewToolFilter(nil, nil, nil, nil, nil),
	}

	if _, err := agent.handleGetAPISpec(context.Background(), map[string]interface{}{
		"server_name": "custom",
		"tool_name":   []interface{}{"get_pulse_module_state", "no_such_tool"},
	}); err == nil {
		t.Fatalf("an unknown tool name was silently accepted")
	}
}

// server_name can be wrong in two ways. "custom" is not a category at all;
// "auto_improvement" is a real category that simply does not hold the requested
// tool. Only the first was handled at first, because a valid-but-wrong category
// leaves isCustomCategory true and skips the fallback — so a Pulse Fixer walked
// the category list on 2026-08-01, failing six times in a row asking for
// workflow tools under auto_improvement.
func TestGetAPISpecResolvesToolsUnderTheWrongValidCategory(t *testing.T) {
	agent := &Agent{
		toolRegistry: directToolRegistry(
			customToolFixture("get_pulse_module_state", "workflow"),
			customToolFixture("get_reference_doc", "auto_improvement"),
		),
		toolFilter: NewToolFilter(nil, nil, nil, []string{"workflow", "auto_improvement"}, nil),
	}

	spec, err := agent.handleGetAPISpec(context.Background(), map[string]interface{}{
		"server_name": "auto_improvement",
		"tool_name":   "get_pulse_module_state",
	})
	if err != nil {
		t.Fatalf("workflow tool requested under auto_improvement failed: %v", err)
	}
	if !strings.Contains(spec, "get_pulse_module_state") {
		t.Fatalf("spec is missing the requested tool:\n%s", spec)
	}
}

// Name-based resolution reads the canonical registry directly, while the tool index
// applies isToolAllowed. Without the same check here, resolving by name hands
// back the spec for a tool the session was deliberately denied —
// SetToolAccess is exactly how a Pulse Fixer stage is bounded.
func TestGetAPISpecDoesNotDiscloseDeniedTools(t *testing.T) {
	agent := &Agent{
		toolRegistry: directToolRegistry(
			customToolFixture("get_pulse_module_state", "workflow"),
			customToolFixture("mark_pulse_final_command_result", "workflow"),
		),
		toolFilter:    NewToolFilter(nil, nil, nil, []string{"workflow"}, nil),
		toolAllowList: map[string]bool{"get_pulse_module_state": true},
	}

	if _, err := agent.handleGetAPISpec(context.Background(), map[string]interface{}{
		"server_name": "custom",
		"tool_name":   "mark_pulse_final_command_result",
	}); err == nil {
		t.Fatalf("returned the spec for a tool excluded from this session's allow list")
	}
}

// Partial resolution reads as success. An agent that asked for two tools and
// silently received one has no way to see which is missing until it calls it.
func TestGetAPISpecFailsAtomicallyOnAPartlyResolvableRequest(t *testing.T) {
	agent := &Agent{
		toolRegistry: directToolRegistry(customToolFixture("get_pulse_module_state", "workflow")),
		toolFilter:   NewToolFilter(nil, nil, nil, []string{"workflow"}, nil),
	}

	_, err := agent.handleGetAPISpec(context.Background(), map[string]interface{}{
		"server_name": "workflow",
		"tool_name":   []interface{}{"get_pulse_module_state", "no_such_tool"},
	})
	if err == nil {
		t.Fatalf("a partly resolvable request returned a partial spec instead of failing")
	}
	if !strings.Contains(err.Error(), "no_such_tool") {
		t.Fatalf("error does not name the unresolvable tool: %v", err)
	}
}

// The contract's endpoint is $MCP_CUSTOM/{tool_name} with no category segment,
// so requiring server_name forced a model to supply a value it could not know
// and had to guess — 46 failed get_api_spec calls in one day. Omitting it must
// resolve by tool name alone.
func TestGetAPISpecResolvesWithNoServerNameAtAll(t *testing.T) {
	agent := &Agent{
		toolRegistry: directToolRegistry(
			customToolFixture("get_pulse_module_state", "workflow"),
			customToolFixture("get_pulse_finding_backlog", "workflow"),
		),
		toolFilter: NewToolFilter(nil, nil, nil, []string{"workflow"}, nil),
	}

	spec, err := agent.handleGetAPISpec(context.Background(), map[string]interface{}{
		"tool_name": []interface{}{"get_pulse_module_state", "get_pulse_finding_backlog"},
	})
	if err != nil {
		t.Fatalf("omitting server_name failed: %v", err)
	}
	for _, want := range []string{"get_pulse_module_state", "get_pulse_finding_backlog"} {
		if !strings.Contains(spec, want) {
			t.Fatalf("spec is missing %q:\n%s", want, spec)
		}
	}
}

// tool_name remains genuinely required; dropping server_name must not make the
// call answerable with no address at all.
func TestGetAPISpecStillRequiresToolName(t *testing.T) {
	agent := &Agent{
		toolRegistry: directToolRegistry(customToolFixture("x", "workflow")),
		toolFilter:   NewToolFilter(nil, nil, nil, []string{"workflow"}, nil),
	}

	if _, err := agent.handleGetAPISpec(context.Background(), map[string]interface{}{}); err == nil {
		t.Fatalf("a request with neither server_name nor tool_name was accepted")
	}
}

// Cache entries contain schemas, not authorization decisions. Policy must be
// checked before a hit is returned, including after an allow-list change.
func TestGetAPISpecValidatesPolicyBeforeCacheLookup(t *testing.T) {
	agent := &Agent{
		toolRegistry:     directToolRegistry(customToolFixture("mutate_records", "database")),
		toolFilter:       NewToolFilter(nil, nil, nil, []string{"database"}, nil),
		toolAllowList:    map[string]bool{"query_records": true},
		openAPISpecCache: map[string][]byte{"tools:mutate_records": []byte("FORBIDDEN CACHED SPEC")},
	}

	if spec, err := agent.handleGetAPISpec(context.Background(), map[string]interface{}{
		"tool_name": "mutate_records",
	}); err == nil {
		t.Fatalf("denied tool escaped through schema cache: %s", spec)
	}
}

func TestGetAPISpecResolvesMCPToolByNameOnly(t *testing.T) {
	agent := &Agent{
		tools: []llmtypes.Tool{toolFixture("search_issues")},
		toolToServer: map[string]string{
			"search_issues": "github-server",
		},
		toolFilter: NewToolFilter(nil, nil, nil, nil, nil),
	}

	spec, err := agent.handleGetAPISpec(context.Background(), map[string]interface{}{
		"tool_name": "search_issues",
	})
	if err != nil {
		t.Fatalf("name-only MCP resolution failed: %v", err)
	}
	if !strings.Contains(spec, "/tools/mcp/github_server/search_issues") {
		t.Fatalf("MCP spec used the wrong internal route:\n%s", spec)
	}
}

func TestGetAPISpecCompatibilityServerCannotChangeRouting(t *testing.T) {
	agent := &Agent{
		tools: []llmtypes.Tool{toolFixture("search_issues")},
		toolToServer: map[string]string{
			"search_issues": "github-server",
		},
		toolFilter: NewToolFilter(nil, nil, nil, nil, nil),
	}

	spec, err := agent.handleGetAPISpec(context.Background(), map[string]interface{}{
		"server_name": "wrong-but-valid-category",
		"tool_name":   "search_issues",
	})
	if err != nil {
		t.Fatalf("compatibility server_name changed name-based resolution: %v", err)
	}
	if !strings.Contains(spec, "/tools/mcp/github_server/search_issues") {
		t.Fatalf("compatibility server_name changed the internal MCP route:\n%s", spec)
	}
}

// Agents copied the server name with its credential scope and mixed the spellings of two
// lists (PLAT-856): discovery and the spec now show one short name, with no internal ID.
func TestDiscoveryShowsTheDeclaredAliasNotTheScopedServer(t *testing.T) {
	const scoped = "vault_c-94acea49__scope_86accc00"
	agent := &Agent{
		tools:            []llmtypes.Tool{toolFixture("notion__notion-fetch")},
		toolToServer:     map[string]string{"notion__notion-fetch": scoped},
		mcpToolRealNames: map[string]string{"notion__notion-fetch": "notion_c-94acea49__notion-fetch"},
		mcpServerAliases: map[string]string{scoped: "notion"},
		toolFilter:       NewToolFilter(nil, nil, nil, nil, nil),
	}
	found, err := agent.handleSearchTools(context.Background(), map[string]interface{}{"query": "notion fetch"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(found, `"name":"notion__notion-fetch"`) || !strings.Contains(found, `"server_name":"notion"`) || strings.Contains(found, "__scope_") {
		t.Fatalf("search_tools showed an internal name: %s", found)
	}
	for _, asked := range []string{"notion__notion-fetch", "notion__notion_fetch", "notion_c_94acea49__notion-fetch"} {
		spec, err := agent.handleGetAPISpec(context.Background(), map[string]interface{}{"tool_name": asked, "server_name": "notion"})
		if err != nil {
			t.Fatalf("spec for %q: %v", asked, err)
		}
		if !strings.Contains(spec, "/tools/mcp/notion/notion__notion-fetch") || strings.Contains(spec, "__scope_") {
			t.Fatalf("spec for %q showed another name:\n%s", asked, spec)
		}
	}
}
