package server

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The immutable tool admission lists are what the runtime enforces before
// discovery. A shared panel must not promise another product's tools.
func TestPanelToolMappingsRespectProductAdmission(t *testing.T) {
	goalNames := agentworksproduct.ChatSkills("builder")
	admitted := agentworksproduct.ChatTools("builder")
	public := map[string]bool{}
	for _, source := range admitted {
		public[platformtools.Identity(source)] = true
		public[platformtools.PublicName(source)] = true
	}
	for _, name := range goalNames {
		if !skills.IsWorkspacePanelSkill(name) {
			continue
		}
		mapped := skills.WorkspacePanelToolMap(name)["agentworks"]
		if len(mapped) == 0 {
			t.Fatalf("%s %s has no usable tool mapping", "agentworks", name)
		}
		for _, tool := range mapped {
			if !public[tool] {
				t.Fatalf("%s %s advertises unavailable %s", "agentworks", name, tool)
			}
		}
	}
	panel := skills.BuiltinDefinition("panel-channels")
	if panel == nil {
		t.Fatal("missing shared Google panel procedure")
	}
	if got := skills.WorkspacePanelToolGuidance("manage_google_account", "Account actions", nil); got != "Account actions" {
		t.Fatal("hint advertises an unattached skill")
	}
	// All transports discover this hint on the same finalized tool description.
	hint := skills.WorkspacePanelToolGuidance("manage_google_account", "Account actions", []*llmtypes.Skill{panel})
	if !strings.Contains(hint, "read_skill") || !strings.Contains(hint, panel.Name) {
		t.Fatal("tool cannot discover its shared panel procedure")
	}
}
