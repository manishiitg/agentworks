package handlers

import (
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/workspaceref"
)

// A reader's shell in someone else's Crew runs as the Crew owner's slot (PLAT-810, owner decision 2026-10-10), because the
// Crew folder belongs to the owner's slot group and the reader's own slot cannot enter it. Every Crew lives in the shared
// root, Crew/<slug>-<id8>, which has no owner in its path (the owner is in the server's registry, which this service
// cannot read). The rule applies only when the server's folder guard grants that exact Crew; Landlock still confines the
// command to the guard's paths. Anything else keeps the caller's slot.

// sharedCrewDirForCommand returns the physical folder of the shared-root Crew (Crew/<slug>-<id8>) a command works in
// when the server's folder guard grants that very Crew, and "" otherwise. The shared root has no owner in its path
// (the owner is in the server's registry, which this service cannot read), so the caller finds the owner's slot from
// the folder's group (slotOwningGroup): a Crew folder is group-owned by its owner's slot, mode 2770.
func sharedCrewDirForCommand(docsDir, workingDir string, guard *models.FolderGuardConfig) string {
	if guard == nil || !guard.Enabled {
		return ""
	}
	rel, err := filepath.Rel(docsDir, workingDir)
	if err != nil {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 || parts[0] != workspaceref.SharedCrewRoot || parts[1] == "" || parts[1] == "." || parts[1] == ".." {
		return ""
	}
	crewRoot := workspaceref.SharedCrewRoot + "/" + parts[1]
	for _, granted := range append(append([]string{}, guard.ReadPaths...), guard.WritePaths...) {
		granted = strings.Trim(filepath.ToSlash(granted), "/")
		if filepath.IsAbs(granted) {
			if r, relErr := filepath.Rel(docsDir, granted); relErr == nil {
				granted = filepath.ToSlash(r)
			}
		}
		if granted == crewRoot || strings.HasPrefix(granted, crewRoot+"/") {
			return filepath.Join(docsDir, filepath.FromSlash(crewRoot))
		}
	}
	return ""
}
