package mcpagent

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type definitionObserver struct{}

func (definitionObserver) HandleEvent(context.Context, *events.AgentEvent) error { return nil }
func (definitionObserver) Name() string                                          { return "definition-observer" }

func TestTurnToolPolicyControlsDiscoveryWithoutMutatingDefinition(t *testing.T) {
	agent := &Agent{
		systemPrompt:         "inspect the workflow",
		useCodeExecutionMode: true,
		toolToServer:         make(map[string]string),
		toolFilter:           NewToolFilter(nil, nil, nil, nil, nil),
	}
	registry := newCanonicalToolRegistry()
	for _, name := range []string{"allowed_tool", "blocked_tool"} {
		definition := llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: name}}
		if err := registry.register(registeredTool{
			Name:         name,
			Definition:   definition,
			Kind:         toolImplementationDirect,
			Source:       "direct",
			DisplayGroup: "workflow",
		}); err != nil {
			t.Fatal(err)
		}
	}
	agent.toolRegistry = registry

	policy, err := normalizeToolPolicy(ToolPolicy{AllowedTools: []string{"allowed_tool"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), turnPolicyContextKey{}, policy)
	prompt := agent.outgoingSystemPromptForContext(ctx)
	if strings.Contains(prompt, "allowed_tool") || strings.Contains(prompt, "blocked_tool") || !strings.Contains(prompt, "search_tools") {
		t.Fatalf("prompt must use discovery without listing tools: %s", prompt)
	}
	available := discoveryResult(t, agent, ctx, nil)
	if !strings.Contains(available, "allowed_tool") || strings.Contains(available, "blocked_tool") {
		t.Fatalf("turn policy not enforced by discovery: %s", available)
	}
	if _, err := agent.handleGetAPISpec(ctx, map[string]interface{}{"tool_name": "allowed_tool"}); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.handleGetAPISpec(ctx, map[string]interface{}{"tool_name": "blocked_tool"}); err == nil {
		t.Fatal("blocked tool became accessible after removing the prompt inventory")
	}
	if _, ok := agent.toolRegistry.lookup("blocked_tool"); !ok {
		t.Fatal("runtime policy mutated the canonical definition")
	}
}

func TestCanonicalToolRegistryRejectsDifferentOwners(t *testing.T) {
	registry := newCanonicalToolRegistry()
	definition := llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: "query"}}
	if err := registry.register(registeredTool{Name: "query", Definition: definition, Kind: toolImplementationMCP, Source: "database"}); err != nil {
		t.Fatal(err)
	}
	err := registry.register(registeredTool{Name: "query", Definition: definition, Kind: toolImplementationDirect, Source: "direct"})
	if err == nil || !strings.Contains(err.Error(), `owned by mcp "database"`) {
		t.Fatalf("collision error = %v", err)
	}
}

func TestNormalizeToolPolicyRejectsWhitespace(t *testing.T) {
	_, err := normalizeToolPolicy(ToolPolicy{AllowedTools: []string{"query "}})
	if err == nil || !strings.Contains(err.Error(), "surrounding whitespace") {
		t.Fatalf("normalizeToolPolicy() error = %v", err)
	}
}

func TestDefinitionSnapshotsOnlyIdentityInputs(t *testing.T) {
	agent := &Agent{
		systemPrompt: "base",
		toolRegistry: newCanonicalToolRegistry(),
	}
	agent.appendedSystemPrompts = []string{"supplement", "supplement"}
	agent.attachedSkills = []*llmtypes.Skill{{Name: "workflow", Description: "original"}}
	agent.listeners = []AgentEventListener{definitionObserver{}}

	view := agent.Definition()
	if view.Instructions != "base\n\nsupplement" {
		t.Fatalf("definition instructions = %q", view.Instructions)
	}
	if len(view.SkillDefinitions) != 1 || view.SkillDefinitions[0].Name != "workflow" {
		t.Fatalf("definition skills = %#v", view.SkillDefinitions)
	}
	view.SkillDefinitions[0].Description = "mutated"
	if agent.attachedSkills[0].Description != "original" {
		t.Fatal("definition exposed mutable skill state")
	}
}
