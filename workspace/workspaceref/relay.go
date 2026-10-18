package workspaceref

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
)

// RelayReleaseRoot is the stable snapshot namespace belonging to a draft.
// Keep the original workspace spelling: existing published versions use its hash.
func RelayReleaseRoot(workspace string) string {
	sum := sha256.Sum256([]byte(workspace))
	return path.Join("Workflow", ".relay_releases", hex.EncodeToString(sum[:8]))
}
