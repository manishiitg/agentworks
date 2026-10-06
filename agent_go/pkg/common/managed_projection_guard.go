package common

import (
	"os"
	"path/filepath"
	"strings"
)

// managedCodingAgentProjectionFiles are the instruction files the adapters write at the workspace root.
var managedCodingAgentProjectionFiles = []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"}

// managedCodingAgentProjectionChildren are the files and folders each CLI reads its policy from. Everything here is
// ours: the adapter writes it every turn and a tool that changed it would change the policy the CLI runs under. They
// are protected by name. The Linux backends mount an existing file read-only and skip one that is missing, so a name
// that does not exist yet is covered only where the backend denies by path (Seatbelt); the adapters create most of
// these every turn.
//
// The folders themselves are NOT protected, and neither is their skills/ folder: that is where a person's own skills
// are installed (npx skills add writes to .agents/skills). A projected skill carries an ownership marker and is the only
// kind the adapters remove, so a skill a person installs there is left alone.
var managedCodingAgentProjectionChildren = map[string][]string{
	".agents": {"rules"},
	".claude": {"settings.json", "settings.local.json", "CLAUDE.md", "commands", "agents", "hooks", "rules"},
	".codex":  {"config.toml", "hooks.json", "prompts", "AGENTS.md"},
	".cursor": {"rules", "mcp.json", "hooks.json", "cli.json", "commands"},
	".gemini": {"settings.json", "GEMINI.md", "commands"},
	".pi":     {"settings.json", "APPEND_SYSTEM.md", "SYSTEM.md", "prompts", "extensions"},
}

// managedProjectionRelatives lists the workspace-relative paths to protect: the root files, the named children, and
// whatever else already sits in a projection folder (except skills/), so an adapter's file the list above does not
// know is still covered when it exists.
func managedProjectionRelatives(workspaceRoot string) []string {
	out := append([]string{}, managedCodingAgentProjectionFiles...)
	for dir, children := range managedCodingAgentProjectionChildren {
		names := append([]string{}, children...)
		if entries, err := os.ReadDir(filepath.Join(workspaceRoot, dir)); err == nil {
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
		}
		for _, name := range names {
			if name != "skills" {
				out = append(out, filepath.Join(dir, name))
			}
		}
	}
	return DeduplicateStrings(out)
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
	blocked := append([]string{}, current.BlockedWritePaths...)
	for _, relative := range managedProjectionRelatives(workspaceRoot) {
		blocked = append(blocked, filepath.Join(workspaceRoot, relative), relative)
	}
	SetSessionFolderGuardBlockedWritePaths(sessionID, DeduplicateStrings(blocked))
}

// CodingAgentProjectionBlockedWrites is the blocked-write list
// ProtectCodingAgentProjectionWrites adds for workspaceRoot, for callers that
// build a Folder Guard without a session (a Code workspace's plain shell).
func CodingAgentProjectionBlockedWrites(workspaceRoot string) []string {
	var blocked []string
	for _, relative := range managedProjectionRelatives(workspaceRoot) {
		blocked = append(blocked, filepath.Join(workspaceRoot, relative), relative)
	}
	return blocked
}
