package server

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

// The immutable tool admission lists are what the runtime enforces before
// discovery. A shared panel must not promise another product's tools.
func TestPanelToolMappingsRespectProductAdmission(t *testing.T) {
	goalNames := agentworksproduct.ChatSkills("builder")
	relaySkills, err := relayproduct.ChatSkills("builder")
	if err != nil {
		t.Fatal(err)
	}
	relayNames := []string{}
	for _, skill := range relaySkills {
		relayNames = append(relayNames, skill.Name)
	}
	for product, names := range map[string][]string{"agentworks": goalNames, "relays": relayNames} {
		admitted := agentworksproduct.ChatTools("builder")
		if product == "relays" {
			admitted, err = relayproduct.ChatTools("builder")
			if err != nil {
				t.Fatal(err)
			}
		}
		public := map[string]bool{}
		for _, source := range admitted {
			public[platformtools.Identity(source)] = true
			public[platformtools.PublicName(source)] = true
		}
		for _, name := range names {
			if !skills.IsWorkspacePanelSkill(name) {
				continue
			}
			mapped := skills.WorkspacePanelToolMap(name)[product]
			if len(mapped) == 0 {
				t.Fatalf("%s %s has no usable tool mapping", product, name)
			}
			for _, tool := range mapped {
				if !public[tool] {
					t.Fatalf("%s %s advertises unavailable %s", product, name, tool)
				}
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
	hint := skills.WorkspacePanelToolGuidance("manage_google_account", "Account actions", relaySkills)
	if !strings.Contains(hint, "read_skill") || !strings.Contains(hint, panel.Name) {
		t.Fatal("tool cannot discover its shared panel procedure")
	}
}
