package workflowtypes

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"regexp"
	"strings"
)

var relayReleaseVersion = regexp.MustCompile(`^v[1-9][0-9]*$`)

// RelayReleaseRoot is the stable snapshot namespace belonging to a draft.
func RelayReleaseRoot(workspace string) string {
	sum := sha256.Sum256([]byte(workspace))
	return path.Join("Workflow", ".relay_releases", hex.EncodeToString(sum[:8]))
}

// RelayReleaseWorkspace returns the version root of a snapshot path, including
// paths below it. The manifest-less namespace itself is never a workflow.
func RelayReleaseWorkspace(workspace string) string {
	parts := strings.Split(workspace, "/")
	if len(parts) < 4 || parts[0] != "Workflow" || parts[1] != ".relay_releases" || parts[2] == "" || !relayReleaseVersion.MatchString(parts[3]) {
		return ""
	}
	return strings.Join(parts[:4], "/")
}
