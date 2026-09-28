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
	for _, path := range []string{filepath.Join(root, "existing"), filepath.Join(root, ".agents"), ".agents", filepath.Join(root, "GEMINI.md"), "GEMINI.md"} {
		if !slices.Contains(guard.BlockedWritePaths, path) {
			t.Fatalf("missing blocked write path %q: %v", path, guard.BlockedWritePaths)
		}
	}
}
