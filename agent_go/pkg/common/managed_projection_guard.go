package common

import (
	"path/filepath"
	"strings"
)

var managedCodingAgentProjectionWritePaths = []string{
	"AGENTS.md", "CLAUDE.md", "GEMINI.md", ".agents", ".claude",
	".codex", ".cursor", ".gemini", ".pi",
}

// ProtectCodingAgentProjectionWrites is called at coding-agent bridge setup,
// including chat, delegation, agentsession and workflow paths. The adapters
// own these files; tools may read them but may not change the active policy.
func ProtectCodingAgentProjectionWrites(sessionID, workspaceRoot string) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(workspaceRoot) == "" {
		return
	}
	blocked := make([]string, 0, len(managedCodingAgentProjectionWritePaths)*2)
	if current := GetSessionShellConfig(sessionID); current != nil {
		blocked = append(blocked, current.BlockedWritePaths...)
	}
	for _, relative := range managedCodingAgentProjectionWritePaths {
		blocked = append(blocked, filepath.Join(workspaceRoot, relative), relative)
	}
	SetSessionFolderGuardBlockedWritePaths(sessionID, DeduplicateStrings(blocked))
}
