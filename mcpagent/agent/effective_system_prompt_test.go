package mcpagent

import (
	"context"
	"strings"
	"testing"

	llm "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func codeExecutionPromptAgent() *Agent {
	return &Agent{
		useCodeExecutionMode: true,
		toolRegistry:         newCanonicalToolRegistry(),
		toolToServer:         make(map[string]string),
		toolFilter:           NewToolFilter(nil, nil, nil, nil, nil),
	}
}

func TestEffectiveSystemPromptDiscoversToolsRegisteredAfterSet(t *testing.T) {
	a := codeExecutionPromptAgent()
	a.setInstructions("operator policy")
	addDirectToolFixture(t, a, directToolFixture("query_records", "database"))

	got := a.instructions()
	if strings.Contains(got, "query_records") || !strings.Contains(got, "search_tools") || !strings.Contains(got, "get_api_spec") {
		t.Fatalf("prompt must describe discovery without embedding the inventory:\n%s", got)
	}
	if !strings.Contains(discoveryResult(t, a, context.Background(), nil), "query_records") {
		t.Fatal("late registration did not reach live discovery")
	}
	if a.systemPrompt != "operator policy" {
		t.Fatalf("rendering mutated the stored prompt: %q", a.systemPrompt)
	}
}

func TestEffectiveSystemPromptTracksAllowListChanges(t *testing.T) {
	a := codeExecutionPromptAgent()
	addDirectToolFixture(t, a, directToolFixture("query_records", "database"))
	addDirectToolFixture(t, a, directToolFixture("mutate_records", "database"))
	a.setInstructions("operator policy")

	a.setToolAccess([]string{"query_records"})
	first := discoveryResult(t, a, context.Background(), nil)
	if !strings.Contains(first, "query_records") || strings.Contains(first, "mutate_records") {
		t.Fatalf("first allow-list was not reflected:\n%s", first)
	}

	a.setToolAccess([]string{"mutate_records"})
	second := discoveryResult(t, a, context.Background(), nil)
	if strings.Contains(second, "query_records") || !strings.Contains(second, "mutate_records") {
		t.Fatalf("changed allow-list was not reflected on the next read:\n%s", second)
	}
	if got := a.instructions(); strings.Contains(got, "query_records") || strings.Contains(got, "mutate_records") {
		t.Fatal("allow-list changes reinserted the tool inventory")
	}
}

func TestEffectiveSystemPromptIsBalancedAndIdempotent(t *testing.T) {
	a := codeExecutionPromptAgent()
	addDirectToolFixture(t, a, directToolFixture("query_records", "database"))
	a.setInstructions("before\n{{TOOL_STRUCTURE}}\nafter\n<available_tools>stale inventory</available_tools>")

	first := a.instructions()
	second := a.instructions()
	if first != second {
		t.Fatalf("repeated reads changed the effective prompt")
	}
	if strings.Contains(first, availableToolsOpenTag) || strings.Contains(first, availableToolsCloseTag) || strings.Contains(first, "stale inventory") {
		t.Fatalf("legacy tool manifest survived recomposition:\n%s", first)
	}
	if strings.Count(first, "<runtime_tools>") != 1 {
		t.Fatal("discovery routing must appear exactly once")
	}
	if strings.Contains(first, "{{TOOL_STRUCTURE}}") {
		t.Fatalf("tool placeholder leaked into the effective prompt")
	}
	if !strings.HasPrefix(first, "before\n") || !strings.Contains(first, "\nafter") {
		t.Fatalf("placeholder position was not preserved:\n%s", first)
	}
}

func TestEnsureSystemPromptUsesDiscoveryWithoutInventory(t *testing.T) {
	a := codeExecutionPromptAgent()
	a.setInstructions("operator policy")
	addDirectToolFixture(t, a, directToolFixture("query_records", "database"))
	addDirectToolFixture(t, a, directToolFixture("mutate_records", "database"))
	a.setToolAccess([]string{"query_records"})

	messages := ensureSystemPrompt(a, nil)
	if len(messages) != 1 || len(messages[0].Parts) != 1 {
		t.Fatalf("unexpected system message shape: %#v", messages)
	}
	textPart, ok := messages[0].Parts[0].(llmtypes.TextContent)
	if !ok {
		t.Fatalf("system message part is %T, want TextContent", messages[0].Parts[0])
	}
	if strings.Contains(textPart.Text, "query_records") || strings.Contains(textPart.Text, "mutate_records") || !strings.Contains(textPart.Text, "search_tools") || !strings.Contains(textPart.Text, "get_api_spec") {
		t.Fatalf("actual conversation system message lost discovery or included an inventory:\n%s", textPart.Text)
	}
}

func TestNonCodeExecutionPromptDoesNotGainManifest(t *testing.T) {
	a := &Agent{}
	a.setInstructions("before {{TOOL_STRUCTURE}} after")

	if got := a.instructions(); got != "before  after" {
		t.Fatalf("non-code prompt should only strip the placeholder, got %q", got)
	}
}

func TestMCPAgentExtensionsNeverReplaceProductBasePrompt(t *testing.T) {
	a := codeExecutionPromptAgent()
	a.setInstructions("PRODUCT BASE: You are the Work assistant.")
	a.appendInstructions("MCP EXTENSION: connected tools")
	a.appendInstructions("RUNTIME EXTENSION: native CLI routing")
	addDirectToolFixture(t, a, directToolFixture("query_records", "database"))

	got := a.instructions()
	if !strings.HasPrefix(got, "PRODUCT BASE: You are the Work assistant.") {
		t.Fatalf("mcpagent replaced or moved the product base prompt:\n%s", got)
	}
	for _, want := range []string{"MCP EXTENSION", "RUNTIME EXTENSION", "search_tools", "get_api_spec"} {
		if !strings.Contains(got, want) {
			t.Fatalf("effective prompt omitted %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "PRODUCT BASE") > strings.Index(got, "MCP EXTENSION") {
		t.Fatal("an mcpagent extension appeared before the product base prompt")
	}
}

func TestReadAgentSystemPromptUsesOutboundComposition(t *testing.T) {
	a := codeExecutionPromptAgent()
	a.setInstructions("PRODUCT POLICY\n{{TOOL_STRUCTURE}}\n<available_tools>stale catalog</available_tools>")
	mustAttachSkill(t, a, &llmtypes.Skill{Name: "test-skill", Description: "Fresh discovery", Content: "ON DEMAND BODY"})
	ctx := context.Background()
	got := ReadAgentSystemPrompt(ctx, a)
	if got != a.outgoingSystemPromptForContext(ctx) {
		t.Fatal("inspection differs from send composer")
	}
	for _, absent := range []string{"<available_tools>", "stale catalog", "{{TOOL_STRUCTURE}}", "ON DEMAND BODY"} {
		if strings.Contains(got, absent) {
			t.Fatalf("inspection leaked %q", absent)
		}
	}
	if !strings.Contains(got, "PRODUCT POLICY") || !strings.Contains(got, "Fresh discovery") {
		t.Fatal("inspection lost identity or attached skill discovery")
	}
	if ReadAgentSystemPrompt(nil, a) != got || ReadAgentSystemPrompt(ctx, nil) != "" {
		t.Fatal("nil input handling")
	}
}

func TestLegacyInventoryIsRemovedForEveryCodingProvider(t *testing.T) {
	for _, contract := range llm.CodingAgentProviderContracts() {
		t.Run(string(contract.Provider), func(t *testing.T) {
			a := codeExecutionPromptAgent()
			a.provider = contract.Provider
			a.setInstructions("PRODUCT IDENTITY\n<available_tools>stale_tool_a</available_tools>\n{{TOOL_STRUCTURE}}")
			a.appendInstructions("USER SUPPLEMENT\n<available_tools>stale_tool_b</available_tools>")
			addDirectToolFixture(t, a, directToolFixture("current_tool", "records"))
			history := []llmtypes.MessageContent{
				{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "<available_tools>old_history_tool</available_tools>"}}},
				{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "continue my work"}}},
			}
			messages := ensureSystemPrompt(a, history)
			text := messages[0].Parts[0].(llmtypes.TextContent).Text
			for _, absent := range []string{"<available_tools>", "stale_tool_a", "stale_tool_b", "old_history_tool", "current_tool", "{{TOOL_STRUCTURE}}"} {
				if strings.Contains(text, absent) {
					t.Fatalf("outgoing prompt leaked %q", absent)
				}
			}
			for _, want := range []string{"PRODUCT IDENTITY", "USER SUPPLEMENT", "search_tools", "get_api_spec"} {
				if !strings.Contains(text, want) {
					t.Fatalf("lost instruction %q", want)
				}
			}
			if len(messages) != 2 || messages[1].Parts[0].(llmtypes.TextContent).Text != "continue my work" {
				t.Fatal("prompt cleanup altered the user's conversation")
			}
		})
	}
}

func TestCodeExecutionRoutingOverrideKeepsDiscoveryAndRestrictions(t *testing.T) {
	a := codeExecutionPromptAgent()
	custom := "Use only the granted tools; native shell is disabled."
	a.bridgeRoutingInstructionsOverride = &custom
	a.setInstructions("PRODUCT IDENTITY")
	got := a.outgoingSystemPrompt()
	for _, want := range []string{custom, "search_tools", "get_api_spec"} {
		if !strings.Contains(got, want) {
			t.Fatalf("routing override lost %q", want)
		}
	}
	if strings.Contains(got, "execute_shell_command") || strings.Contains(got, "MCP_AUTH") {
		t.Fatal("discovery guidance invented a shell grant")
	}
}
