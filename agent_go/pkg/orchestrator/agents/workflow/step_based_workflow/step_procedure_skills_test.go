package step_based_workflow

import (
	"slices"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

// Use the real identity assembler and intrinsic installed-skill resolver. A
// skill must be discoverable without its procedures becoming system text.
func TestStepProceduresResolveOnDemand(t *testing.T) {
	c := newAgentClosingTestOrchestrator(t)
	const session = "step-procedural-skills"
	defer common.ClearSessionShellConfig(session)
	common.SetSessionShellEnv(session, map[string]string{"SECRET_SELECTED": "synthetic-value"})
	c.SetSecrets([]orchestrator.SecretEntry{{Name: "SELECTED", Value: "synthetic-value"}, {Name: "OTHER", Value: "unused-value"}})
	cfg := &agents.OrchestratorAgentConfig{MCPSessionID: session, UseCodeExecutionMode: true}
	identity, supplements := c.assembleStepRuntimeIdentity(cfg, nil, false, []string{"query_workflow_db", "mutate_workflow_db", "agent_browser"}, false, true)
	var names []string
	for _, skill := range identity {
		names = append(names, skill.Name)
	}
	reader := skills.NewInstalledSkillReader("", c.GetWorkspacePath())
	for _, name := range []string{"workflow-database", "workflow-knowledgebase", "workflow-execution", "agent-browser"} {
		if !slices.Contains(names, name) {
			t.Fatalf("missing discoverable skill %s: %v", name, names)
		}
		file, err := reader(name, "SKILL.md")
		if err != nil || file.Content == "" {
			t.Fatalf("skill %s cannot be opened: %v", name, err)
		}
		if strings.Contains(strings.Join(supplements, "\n"), file.Content) {
			t.Fatalf("skill %s was eagerly injected", name)
		}
	}
	for path, marker := range map[string]string{"references/managed-write.md": "param_sets", "references/script-helper.md": "execute_many"} {
		file, err := reader("workflow-database", path)
		if err != nil || !strings.Contains(file.Content, marker) {
			t.Fatalf("missing DB procedure %s: %v", path, err)
		}
	}
	text := strings.Join(supplements, "\n")
	if !strings.Contains(text, "`SELECTED`") || strings.Contains(text, "`OTHER`") || strings.Contains(text, "synthetic-value") || strings.Contains(text, "unused-value") {
		t.Fatal("secret listing diverged from injected environment")
	}
	if strings.Contains(text, "## Bundled Browser Recording") {
		t.Fatal("full browser procedures were injected")
	}
	// Archived flags cannot put one provider back onto full inline procedures.
	if !usesProjectedReferenceSkills(cfg, map[string]string{"UseProjectedReferenceSkills": "false"}) {
		t.Fatal("provider-specific procedural fallback survived")
	}
	t.Logf("attached procedural skills: %v; supplementary system text: %d characters", names, len([]rune(text)))
}
