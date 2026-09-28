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
	// Only add to a guard the session already has. Setting blocked-write
	// paths marks the session guarded, and a guarded session with no read or
	// write paths fails closed: a delegated or background sub-agent whose
	// guard comes from its request context (delegation sets the session guard
	// on the parent) would lose all workspace access ("no workspace read
	// paths were granted"). Such a session keeps its context guard, as before.
	current := GetSessionShellConfig(sessionID)
	if current == nil || (!current.FolderGuardSet && len(current.ReadPaths) == 0 && len(current.WritePaths) == 0) {
		return
	}
	blocked := make([]string, 0, len(managedCodingAgentProjectionWritePaths)*2+len(current.BlockedWritePaths))
	blocked = append(blocked, current.BlockedWritePaths...)
	for _, relative := range managedCodingAgentProjectionWritePaths {
		blocked = append(blocked, filepath.Join(workspaceRoot, relative), relative)
	}
	SetSessionFolderGuardBlockedWritePaths(sessionID, DeduplicateStrings(blocked))
}

// CodingAgentProjectionBlockedWrites is the blocked-write list
// ProtectCodingAgentProjectionWrites adds for workspaceRoot, for callers that
// build a Folder Guard without a session (a Code workspace's plain shell).
func CodingAgentProjectionBlockedWrites(workspaceRoot string) []string {
	blocked := make([]string, 0, len(managedCodingAgentProjectionWritePaths)*2)
	for _, relative := range managedCodingAgentProjectionWritePaths {
		blocked = append(blocked, filepath.Join(workspaceRoot, relative), relative)
	}
	return blocked
}
