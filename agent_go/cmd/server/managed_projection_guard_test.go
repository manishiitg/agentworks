package server

import (
	"slices"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func TestProtectManagedCodingAgentProjectionWritesPreservesExistingDenies(t *testing.T) {
	const sessionID = "managed-projection-guard"
	common.SetSessionFolderGuardBlockedWritePaths(sessionID, []string{"Workflow/demo/planning"})

	protectManagedCodingAgentProjectionWrites(sessionID, "Chats/Work/projects/demo")
	blocked := common.GetSessionShellConfig(sessionID).BlockedWritePaths
	for _, want := range []string{
		"Workflow/demo/planning",
		"AGENTS.md",
		"Chats/Work/projects/demo/AGENTS.md",
		// PLAT-568: the CLI policy files are blocked, not the whole folders (skills/ stays installable).
		".agents/rules",
		"Chats/Work/projects/demo/.agents/rules",
		"CLAUDE.md",
		"Chats/Work/projects/demo/.claude/settings.json",
	} {
		if !slices.Contains(blocked, want) {
			t.Fatalf("managed write guard missing %q: %v", want, blocked)
		}
	}
}
