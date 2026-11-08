package security

import (
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
)

// ResolveShellLogPath uses the same binding, write grants and hard denies as
// brokered artifacts. A log destination never grants another folder access.
func ResolveShellLogPath(destination, base string, guard *models.FolderGuardConfig) (string, error) {
	resolved, bound, err := resolveBrowserArtifactPath(destination, canonicalPath(base))
	if err != nil {
		return "", err
	}
	resolved = canonicalPath(resolved)
	if !bound && !pathWithin(resolved, canonicalPath(base)) {
		return "", fmt.Errorf("log must stay in the workspace")
	}
	if guard != nil && guard.Enabled {
		if !pathAllowedForArtifactWrite(resolved, base, guard.WritePaths) || pathCoveredByGuard(resolved, base, guard.BlockedPaths) || pathCoveredByGuard(resolved, base, guard.BlockedWritePaths) {
			return "", fmt.Errorf("log is outside this command's write grants")
		}
	}
	return resolved, nil
}
