package mcpagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeHTTPSkillLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name  string
		code  bool
		shell bool
	}{
		{"workflow-bridge-shell", true, true}, {"native-crew-without-bridge-shell", true, false}, {"native-api", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := filepath.Join(t.TempDir(), "mcp.json")
			if err := os.WriteFile(config, []byte(`{"mcpServers":{}}`), 0600); err != nil {
				t.Fatal(err)
			}
			a, err := NewAgentFromDefinition(context.Background(), AgentDefinition{Instructions: "product policy", Tools: ToolSet{Direct: []ToolDefinition{{Name: "read_fact", Description: "Read one fact.", InputSchema: map[string]interface{}{"type": "object"}, Execute: func(context.Context, map[string]interface{}) (string, error) { return "fact", nil }}}}}, RuntimeConfig{Model: linkedOutputFakeModel{}, MCPConfigPath: config, Tools: ToolRuntimeConfig{CodeExecution: tc.code}, Coding: CodingRuntimeConfig{BridgeToolAdmit: func(name string) bool { return name != "execute_shell_command" || tc.shell }}})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			want := tc.code
			if a.hasRuntimeHTTPSkill() != want {
				t.Fatal("constructor failed to attach transport guidance without changing inventory/discovery mode")
			}
			if want {
				prompt := a.instructions()
				if !strings.Contains(prompt, "product policy") || !strings.Contains(prompt, runtimeHTTPSkillName) || strings.Contains(prompt, "--fail-with-body") {
					t.Fatal("prompt must preserve policy and route to the skill without inlining its curl tutorial")
				}
				if strings.Contains(prompt, "<available_tools>") || strings.Contains(prompt, "read_fact") {
					t.Fatal("code execution embedded an inventory instead of using live discovery")
				}
				var body string
				for _, skill := range a.attachedSkills {
					if skill.Name == runtimeHTTPSkillName {
						body = skill.Content
					}
				}
				if !tc.shell {
					if strings.Contains(body, "execute_shell_command") || strings.Contains(body, "$MCP_AUTH") || !strings.Contains(body, "call_tool") {
						t.Fatal("native Crew skill advertises an unavailable shell or omits the callable bridge")
					}
					return
				}
				for _, rule := range []string{"get_api_spec", "MCP_AUTH", "--fail-with-body", "nonzero HTTP-failure", "response envelope"} {
					if !strings.Contains(body, rule) {
						t.Fatalf("transport skill lost %s", rule)
					}
				}
				if !strings.Contains(body, "search_tools") {
					t.Fatal("HTTP skill lost discovery")
				}
				a.detachSkill(runtimeHTTPSkillName)
				if !strings.Contains(a.instructions(), "--fail-with-body") {
					t.Fatal("removing the skill must restore inline mechanics, not leave a dangling pointer")
				}
			}
		})
	}
}
