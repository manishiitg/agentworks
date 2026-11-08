package guidance

import (
	"strings"
	"testing"
)

// socialMediaSubAgentTools is the exact registered set reported by the
// `tools_unavailable` error that opened PLAT-125, copied from the log line for
// the msgseq-…-sub-execute-allocate session.
var socialMediaSubAgentTools = []string{
	"agent_browser", "diff_patch_workspace_file", "execute_shell_command",
	"mutate_workflow_db", "query_workflow_db", "read_skill",
	"record_run_concern",
}

// TestStepExecutionSurfaceNeverAdvertisesUnheldProviderTools reproduces the
// original defect. The session above was handed llm-provider-config, followed
// it, called list_published_llms, and failed — then guessed provider names.
func TestStepExecutionSurfaceNeverAdvertisesUnheldProviderTools(t *testing.T) {
	skill := MaterializeStepExecutionReferenceSkill(StepExecutionSignals{ToolNames: socialMediaSubAgentTools})
	if skill == nil {
		t.Fatal("expected a reference skill for a step holding eight tools")
	}

	for _, banned := range []string{"references/llm-provider-config.md", "references/llm-selection.md"} {
		if strings.Contains(skill.Content, banned) {
			t.Errorf("step reference TOC advertises %s; this session registers no provider-configuration tool.\nTOC:\n%s", banned, skill.Content)
		}
		for _, file := range skill.SupportingFiles {
			if file.RelPath == banned {
				t.Errorf("step reference bundle ships %s to a session with no provider-configuration tool", banned)
			}
		}
	}
}
