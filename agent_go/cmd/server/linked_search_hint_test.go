package server

import (
	"strings"
	"testing"
)

// A CLI's own search tools skip a symlinked folder unless it is named (verified with
// ripgrep and Claude Code's Grep and Glob), so the runtime instructions must say to name it.
func TestLinkedRuntimeInstructionsTellTheAgentToSearchProject(t *testing.T) {
	for name, text := range map[string]string{
		"workflow": workflowCLIWorkspaceInstructions("Workflow/demo"),
		"crew":     crewCLIWorkspaceInstructions("Chats/Work/projects/demo"),
	} {
		if !strings.Contains(text, "pass `project`") || !strings.Contains(text, "search path") {
			t.Fatalf("%s instructions lack the search-path warning: %s", name, text)
		}
	}
}

func TestLinkedBuildersUseCurrentPlatformSkillsAndVaultCatalog(t *testing.T) {
	for name, text := range map[string]string{
		"workflow": workflowCLIWorkspaceInstructions("Workflow/demo"),
		"crew":     crewCLIWorkspaceInstructions("Chats/Work/projects/demo"),
	} {
		for _, want := range []string{"current attached builder-reference", "Never guess a provider's skill folder", "Do not search project/ for legacy", "search_tools/get_api_spec", "Shared secrets live in Vault", "backend authorization remain authoritative"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s instructions omit %q", name, want)
			}
		}
	}
}
