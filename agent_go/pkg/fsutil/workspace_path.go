package fsutil

import (
	"fmt"
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/utils"
)

// WorkspacePath resolves an admitted logical path. Its caller has already
// selected any per-user namespace; this helper must not choose a default user.
func WorkspacePath(docs, logical string) (string, error) {
	if resolved, bound, err := crewroot.Resolve(docs, logical); bound || err != nil {
		return resolved, err
	}
	resolved := filepath.Join(docs, logical)
	if !crewroot.Within(docs, resolved) || !utils.IsValidFilePath(resolved, docs) {
		return "", fmt.Errorf("path leaves the workspace")
	}
	return resolved, nil
}
