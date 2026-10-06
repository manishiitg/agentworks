package common

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestProtectCodingAgentProjectionWritesCoversWorkspaceAndRelativePaths(t *testing.T) {
	session := "managed-projection-test"
	root := t.TempDir()
	SetSessionFolderGuard(session, []string{root}, []string{root})
	SetSessionFolderGuardBlockedWritePaths(session, []string{filepath.Join(root, "existing")})
	ProtectCodingAgentProjectionWrites(session, root)
	guard := GetSessionShellConfig(session)
	for _, path := range []string{filepath.Join(root, "existing"), filepath.Join(root, ".agents", "rules"), ".claude/settings.json", filepath.Join(root, "GEMINI.md"), "GEMINI.md"} {
		if !slices.Contains(guard.BlockedWritePaths, path) {
			t.Fatalf("missing blocked write path %q: %v", path, guard.BlockedWritePaths)
		}
	}
	// A person's own skills are installed under <cli>/skills (npx skills add writes .agents/skills): neither the skills
	// folders nor the CLI folders around them are blocked, only our policy files in them (Excellence, 2026-10-06).
	for _, path := range []string{".agents", ".agents/skills", ".claude", ".claude/skills", ".pi/skills", filepath.Join(root, ".agents", "skills")} {
		if slices.Contains(guard.BlockedWritePaths, path) {
			t.Fatalf("%q must stay writable: %v", path, guard.BlockedWritePaths)
		}
	}
}

// A session with no guard of its own (a delegated or background sub-agent,
// whose guard comes from its request context) must stay unguarded: marking it
// guarded with only blocked-write paths made it fail closed and lose all
// workspace access.
func TestProtectCodingAgentProjectionWritesLeavesUnguardedSessionsAlone(t *testing.T) {
	session := "managed-projection-unguarded"
	ClearSessionShellConfig(session)
	t.Cleanup(func() { ClearSessionShellConfig(session) })
	ProtectCodingAgentProjectionWrites(session, t.TempDir())
	if cfg := GetSessionShellConfig(session); cfg != nil && (cfg.FolderGuardSet || len(cfg.BlockedWritePaths) > 0) {
		t.Fatalf("an unguarded session became guarded: %+v", cfg)
	}
	// A session that only carries shell env (no guard) is left alone too.
	SetSessionShellEnv(session, map[string]string{"X": "1"})
	ProtectCodingAgentProjectionWrites(session, t.TempDir())
	if cfg := GetSessionShellConfig(session); cfg == nil || cfg.FolderGuardSet || len(cfg.BlockedWritePaths) > 0 {
		t.Fatalf("an env-only session became guarded: %+v", cfg)
	}
}
