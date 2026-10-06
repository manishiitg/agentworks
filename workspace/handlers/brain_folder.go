package handlers

import (
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/models"
)

// brainFolder is Brain's folder at the top of the documents tree (agent_go cmd/server brainFolderName, PLAT-633).
const brainFolder = "Brain"

// isBrainFolderCommand reports whether a command works in Brain's folder with a folder guard that grants writes there.
// The guard comes from the agent server, which grants Brain/ only to the Brain chat of someone who owns the whole Brain.
func isBrainFolderCommand(docsDir, workingDir string, guard *models.FolderGuardConfig) bool {
	if guard == nil || !guard.Enabled {
		return false
	}
	rel, err := filepath.Rel(docsDir, workingDir)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	if rel != brainFolder && !strings.HasPrefix(rel, brainFolder+"/") {
		return false
	}
	for _, write := range guard.WritePaths {
		if strings.Trim(filepath.ToSlash(write), "/") == brainFolder {
			return true
		}
	}
	return false
}
