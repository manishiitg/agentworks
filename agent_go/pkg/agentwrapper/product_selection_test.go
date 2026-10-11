package agent

import (
	"context"
	"strings"
	"testing"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestBuilderProductSelectionOnFreshAndResumedDefinitions(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "0")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "")
	t.Setenv("AGENT_PRODUCTS", "")
	w := &LLMAgentWrapper{}
	text := "project secrets <!-- product:mcp-gateway -->Vault<!-- /product --> <!-- product:knowledgebase -->brain_read<!-- /product --> local learnings"
	for _, mode := range []string{"fresh", "resumed"} {
		if err := w.ResetInstructions(text); err != nil {
			t.Fatal(err)
		}
		if err := w.AddInstructions(text); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"brain", "vault-access", "workflow-learnings"} {
			if err := w.AttachSkill(&llmtypes.Skill{Name: name, Content: text, Source: llmtypes.SkillSource{Origin: "builtin"}}); err != nil {
				t.Fatal(err)
			}
		}
		for _, name := range []string{"brain_read", "manage_vault_access", "test_relay", "set_workflow_secret", "list_mcp_servers"} {
			if err := w.RegisterCustomTool(name, text, map[string]interface{}{}, func(context.Context, map[string]interface{}) (string, error) { return "called", nil }, "test"); err != nil {
				t.Fatal(err)
			}
		}
		if got := w.AssemblyInstructions(); strings.Contains(got, "Vault") || strings.Contains(got, "brain_read") || !strings.Contains(got, "local learnings") {
			t.Fatalf("%s: %s", mode, got)
		}
		if len(w.definition.Tools.Direct) != 2 || len(w.AttachedSkills()) != 1 {
			t.Fatalf("%s tools=%v skills=%v", mode, w.definition.Tools.Direct, w.AttachedSkills())
		}
	}
	if err := w.SetInstalledSkillResolver(func(string, string) (mcpagent.InstalledSkillFile, error) {
		return mcpagent.InstalledSkillFile{Content: "old Brain guidance"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.installedSkillResolver("brain", "SKILL.md"); err == nil {
		t.Fatal("read_skill resurrected disabled product")
	}
}
func TestCachedToolRechecksInstallation(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "1")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "")
	t.Setenv("AGENT_PRODUCTS", "")
	called := false
	w := &LLMAgentWrapper{}
	if err := w.RegisterCustomTool("brain_read", "read", nil, func(context.Context, map[string]interface{}) (string, error) { called = true; return "ok", nil }, "test"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "0")
	if _, err := w.definition.Tools.Direct[0].Execute(context.Background(), nil); err == nil || called {
		t.Fatal("cached executor bypassed installation gate")
	}
}
