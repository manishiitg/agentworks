package fsutil

import "github.com/manishiitg/coding-agent-loop/workspace/utils"

// WorkspacePath resolves an admitted logical path using the same local Crew
// binding as the workspace service. Callers must check product access first.
func WorkspacePath(docs, logical string) (string, error) {
	return utils.ResolveUserPath(docs, logical, "")
}
