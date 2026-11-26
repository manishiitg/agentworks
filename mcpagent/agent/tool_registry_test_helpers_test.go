package mcpagent

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func directToolFixture(name, displayGroup string) registeredTool {
	return registeredTool{
		Name: name,
		Definition: llmtypes.Tool{
			Type: "function",
			Function: &llmtypes.FunctionDefinition{
				Name:        name,
				Description: name + " description",
			},
		},
		Kind:         toolImplementationDirect,
		Source:       "direct",
		DisplayGroup: displayGroup,
		Executor:     noopTool,
	}
}

func directToolRegistry(tools ...registeredTool) *canonicalToolRegistry {
	registry := newCanonicalToolRegistry()
	for _, tool := range tools {
		if err := registry.register(tool); err != nil {
			panic(err)
		}
	}
	return registry
}

func addDirectToolFixture(t *testing.T, agent *Agent, tool registeredTool) {
	t.Helper()
	if agent.toolRegistry == nil {
		agent.toolRegistry = newCanonicalToolRegistry()
	}
	if err := agent.toolRegistry.register(tool); err != nil {
		t.Fatalf("register direct tool fixture: %v", err)
	}
}
