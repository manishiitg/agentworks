package agents

import (
	"context"
	"strings"
	"testing"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestWorkflowProductSelectionAndFallbackSkillReads(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "0")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "")
	t.Setenv("AGENT_PRODUCTS", "")
	ba := &BaseAgent{}
	text := "project knowledge <!-- product:knowledgebase -->Brain<!-- /product --> local learnings"
	if err := ba.ApplyIdentity(context.Background(), []*llmtypes.Skill{{Name: "brain"}, {Name: "workflow-learnings"}}, text); err != nil {
		t.Fatal(err)
	}
	if len(ba.AttachedSkills()) != 1 || strings.Contains(ba.definition.Instructions, "Brain") {
		t.Fatal("workflow identity retained disabled product")
	}
	ba.SetInstalledSkillResolver(func(string, string) (mcpagent.InstalledSkillFile, error) {
		return mcpagent.InstalledSkillFile{Content: text}, nil
	})
	if _, err := ba.installedSkillResolver("brain", "SKILL.md"); err == nil {
		t.Fatal("workflow fallback resurrected Brain")
	}
	if file, err := ba.installedSkillResolver("agent-browser", "SKILL.md"); err != nil || strings.Contains(file.Content, "Brain") || !strings.Contains(file.Content, "local learnings") {
		t.Fatalf("local skill read: %+v %v", file, err)
	}
	if file, err := ba.installedSkillResolver("my-project-skill", "SKILL.md"); err != nil || file.Content != text {
		t.Fatal("project-authored fallback content was changed")
	}
	called := false
	tool := mcpagent.ToolDefinition{Name: "brain_read", Execute: func(context.Context, map[string]interface{}) (string, error) { called = true; return "ok", nil }}
	if _, allowed := projectProductTool(ba.productSelection, tool); allowed {
		t.Fatal("scheduled/step tool admitted Brain locally")
	}
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "1")
	cached, allowed := projectProductTool(ba.productSelection, tool)
	if !allowed {
		t.Fatal("explicit opt-in did not restore step tool")
	}
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "0")
	if _, err := cached.Execute(context.Background(), nil); err == nil || called {
		t.Fatal("cached step tool bypassed current selection")
	}
}
