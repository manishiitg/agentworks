package handlers

import (
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/workspaceref"
)

// crewProjectOwnerForCommand returns the owner of the shared Crew project (_users/<owner>/Chats/Work/projects/<p>)
// a command works in when the caller is somebody else, and "" otherwise (PLAT-810).
//
// A Crew folder is owned by its owner's slot group, so a reader's own slot cannot even enter it and every shell
// command died with "permission denied". The owner decided (2026-10-10) that a reader's shell in a Crew runs as the
// owner's slot, as a Crew turn does (DECISIONS 2026-10-04). That is only safe because the folder guard still confines
// the command: this applies only when the server's guard grants this very project (a read or write path inside it),
// and Landlock keeps the command to the guard's paths. A guard that does not name the project, a working folder
// outside it, or the owner's own call keeps the caller's slot.
func crewProjectOwnerForCommand(docsDir, workingDir, callerID string, guard *models.FolderGuardConfig) string {
	if guard == nil || !guard.Enabled {
		return ""
	}
	rel, err := filepath.Rel(docsDir, workingDir)
	if err != nil {
		return ""
	}
	ref, ok := workspaceref.Parse(filepath.ToSlash(rel))
	if !ok || !ref.HasOwner() || ref.Owner() == callerID {
		return ""
	}
	logical := ref.Logical()
	prefix := workspaceref.CrewProjectsRoot + "/"
	if !strings.HasPrefix(logical, prefix) {
		return ""
	}
	project := strings.SplitN(strings.TrimPrefix(logical, prefix), "/", 2)[0]
	if project == "" {
		return ""
	}
	projectRoot := "_users/" + ref.Owner() + "/" + workspaceref.CrewProjectsRoot + "/" + project
	for _, granted := range append(append([]string{}, guard.ReadPaths...), guard.WritePaths...) {
		granted = strings.Trim(filepath.ToSlash(granted), "/")
		if filepath.IsAbs(granted) {
			if r, relErr := filepath.Rel(docsDir, granted); relErr == nil {
				granted = filepath.ToSlash(r)
			}
		}
		if granted == projectRoot || strings.HasPrefix(granted, projectRoot+"/") {
			return ref.Owner()
		}
	}
	return ""
}

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
