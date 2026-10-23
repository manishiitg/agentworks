package guidance

import (
	"strings"
	"testing"
)

func TestWorkflowGuideIsDiscoverableAndKeepsHelpNonMutating(t *testing.T) {
	for _, mode := range []string{"workshop", "run"} {
		t.Run(mode, func(t *testing.T) {
			skill := MaterializeReferenceSkill(mode)
			const referencePath = "references/workflow-guide.md"
			if skill == nil || !strings.Contains(skill.Content, referencePath) {
				t.Fatal("workflow help guide is not discoverable")
			}
			content := materializedFileContent(t, skill, referencePath)
			for _, required := range []string{
				"Answer the user's question first",
				"Do not edit, configure, approve, or run anything unless the user asks",
				"**Plan**",
				"**Dashboard**",
				"**Autopilot**",
			} {
				if !strings.Contains(content, required) {
					t.Errorf("workflow guide is missing %q", required)
				}
			}
		})
	}
}
