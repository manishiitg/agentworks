package handlers

import (
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
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

// crewPrivateDirForCommand returns a Crew's private storage root, <docs>/_system/crew-data/<crew id>, when the command
// works inside a folder the server's guard grants in that root, and "" otherwise. A Crew workflow's runs, logs and
// database live there, so a run started by someone else than the owner (a Team member, a Run person) used to start
// as the caller's slot and was refused before its script ran ("working folder: ... permission denied"). A workflow run
// is a Crew run and uses the Crew's account (PLAT-746-355): like the Crew folder, the private root is group-owned by
// the owner's slot (mode 2770), so the caller finds that slot with slotOwningGroup. Landlock still confines the command
// to the guard's paths.
func crewPrivateDirForCommand(docsDir, workingDir string, guard *models.FolderGuardConfig) string {
	if guard == nil || !guard.Enabled {
		return ""
	}
	rel, err := filepath.Rel(docsDir, workingDir)
	if err != nil {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	prefix := strings.Split(crewroot.PrivateDirectory, "/")
	if len(parts) <= len(prefix) {
		return ""
	}
	for i, segment := range prefix {
		if parts[i] != segment {
			return ""
		}
	}
	if id := parts[len(prefix)]; id == "" || id == "." || id == ".." {
		return ""
	}
	root := filepath.Join(docsDir, crewroot.PrivateDirectory, parts[len(prefix)])
	for _, granted := range append(append([]string{}, guard.ReadPaths...), guard.WritePaths...) {
		physical, resolveErr := resolveGuardWritePath(granted, docsDir)
		if resolveErr != nil {
			continue
		}
		physical = filepath.Clean(physical)
		if pathInside(root, physical) && pathInside(physical, workingDir) {
			return root
		}
	}
	return ""
}

// pathInside reports whether path is root or below it.
func pathInside(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel)
}
