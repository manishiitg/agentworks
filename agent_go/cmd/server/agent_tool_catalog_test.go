package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// PLAT-825-113: grouped sources must preserve every actually offered enum,
// including non-action selectors and payload fields named scope.
func TestAgentSourceActionsKeepRealSchemasAndPayloadScope(t *testing.T) {
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range catalog {
		var key string
		for _, action := range platformtools.AgentActions {
			if action.Source == source.Name {
				key = action.SourceParameter()
				break
			}
		}
		if key == "" {
			continue
		}
		props, _ := source.InputSchema["properties"].(map[string]any)
		selector, _ := props[key].(map[string]any)
		raw, _ := json.Marshal(selector["enum"])
		var choices []string
		if err := json.Unmarshal(raw, &choices); err != nil {
			t.Fatalf("%s source enum: %v", source.Name, err)
		}
		if len(choices) == 0 {
			t.Fatalf("%s has no %s enum", source.Name, key)
		}
		for _, choice := range choices {
			found := false
			for _, action := range platformtools.AgentActions {
				if action.Source == source.Name && action.SourceAction == choice {
					if _, err := platformtools.MemberSchema(source.InputSchema, action); err != nil {
						t.Fatal(err)
					}
					found = true
				}
			}
			if !found {
				t.Errorf("%s %s=%s has no grouped action", source.Name, key, choice)
			}
		}
	}
	var source externalTool
	for _, tool := range catalog {
		if tool.Name == "manage_vault_secret_access" {
			source = tool
		}
	}
	if source.Name == "" {
		t.Fatal("real Vault schema missing")
	}
	seen := map[string]any{}
	projected, err := platformtools.Project([]mcpagent.ToolDefinition{{Name: source.Name, InputSchema: source.InputSchema,
		Execute: func(_ context.Context, args map[string]any) (string, error) {
			seen = args
			return "stored names only", nil
		}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range projected {
		if tool.Name != "vault" {
			continue
		}
		_, err = tool.Execute(t.Context(), map[string]any{"action": "set_value", "scope": "shared", "values": map[string]any{"EXAMPLE_KEY": "synthetic-secret"}, "operation": "delete"})
		// The fixed source selector is private and cannot be supplied by a caller.
		if err == nil {
			t.Fatal("public call accepted a source selector override")
		}
		_, err = tool.Execute(t.Context(), map[string]any{"action": "set_value", "scope": "shared", "values": map[string]any{"EXAMPLE_KEY": "synthetic-secret"}})
		if err != nil {
			t.Fatalf("valid Vault payload scope refused: %v", err)
		}
		if seen["operation"] != "set_value" || seen["scope"] != "shared" {
			t.Fatal("source selector or payload scope changed")
		}
		return
	}
	t.Fatal("Vault group missing")
}
