package server

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// managedCodingAgentProjectionWritePaths are owned by the active coding-agent
// adapter. They carry the assembled system prompt, selected skills, MCP
// configuration and provider runtime metadata. Agents may read them, but must
// not mutate them as ordinary project files.
var managedCodingAgentProjectionWritePaths = []string{
	"AGENTS.md",
	"CLAUDE.md",
	"GEMINI.md",
	".agents",
	".claude",
	".codex",
	".cursor",
	".gemini",
	".pi",
}

func protectManagedCodingAgentProjectionWrites(sessionID, workspaceRoot string) {
	common.ProtectCodingAgentProjectionWrites(sessionID, workspaceRoot)
}
