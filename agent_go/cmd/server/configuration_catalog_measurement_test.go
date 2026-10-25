package server

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// PLAT-825-113: observe actual registered schemas in the existing fixtures.
// Conditional grants absent from a fixture stay absent. These affected-family
// envelopes exclude SDK intrinsics and are not universal chat token counts.
func logConfigurationCatalog(t *testing.T, surface string, tools map[string]recordedTool) {
	t.Helper()
	if !strings.HasPrefix(surface, "external-") {
		copy := &recordingRegistrar{tools: map[string]recordedTool{}}
		for name, tool := range tools {
			copy.tools[name] = tool
		}
		projectRecordedCatalog(t, copy)
		tools = copy.tools
	}
	families := map[string][]string{
		"settings": {"settings", "get_workflow_config", "update_workflow_config", "update_variable", "chat_preferences"},
		"models":   {"models", "get_llm_config", "list_published_llms", "list_provider_models", "list_llm_capabilities", "test_llm", "set_workflow_llm_config", "generate_text_llm", "manage_crew_model"},
		"costs":    {"costs", "get_cost_summary", "query_workflow_costs", "get_crew_costs"},
	}
	for _, family := range []string{"settings", "models", "costs"} {
		names := []string{}
		for _, name := range families[family] {
			if _, found := tools[name]; found {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		envelope := []map[string]any{}
		for _, name := range names {
			tool := tools[name]
			envelope = append(envelope, map[string]any{"name": name, "description": tool.desc, "inputSchema": tool.params})
		}
		encoded, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("CONFIGURATION_SCHEMA surface=%s family=%s tools=%d bytes=%d", surface, family, len(names), len(encoded))
	}
}

func logExternalConfigurationCatalog(t *testing.T) {
	t.Helper()
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]recordedTool{}
	for _, tool := range externalListedTools(&UserClaims{UserID: "owner"}, catalog) {
		tools[tool.Name] = recordedTool{desc: tool.Description, params: tool.InputSchema}
	}
	logConfigurationCatalog(t, "external-mcp-owner", tools)
}
