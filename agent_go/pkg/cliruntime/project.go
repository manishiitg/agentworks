package cliruntime

import (
	"fmt"
	"os"
	"path/filepath"
)

// ProjectLink is the single data entry point in a private runtime. Linking the
// directory, rather than individual files, preserves create/rename/delete and
// atomic writes without a synchronizer. Generated CLI files stay beside it.
const ProjectLink = "project"

// PrepareLinkedProject keeps instructions private and project data authoritative.
// The caller must authorize project before calling, and grant its real path
// separately to the sandbox: a writable runtime does not grant its link target.
func PrepareLinkedProject(stateRoot, workspaceRoot, user, project, session, provider, mode string) (string, error) {
	return PrepareLinkedProjectMoved(stateRoot, workspaceRoot, user, project, "", session, provider, mode)
}

// PrepareLinkedProjectMoved is PrepareLinkedProject for a project that has moved inside the workspace root (a Crew moved
// to the shared root, PLAT-442 step 4). legacyRel is the workspace-relative path the project had when the runtime was
// first made: it, not the new path, is the digest input, so the runtime folder (and the native CLI session that lives
// by its path) is the one the project's chats already use. The runtime's project link, which still points at the old
// folder, is repointed to the new one, and only from exactly that old target; any other changed link still fails
// the launch. An empty legacyRel is an ordinary project.
func PrepareLinkedProjectMoved(stateRoot, workspaceRoot, user, project, legacyRel, session, provider, mode string) (string, error) {
	target, err := filepath.EvalSymlinks(project)
	if err != nil {
		return "", fmt.Errorf("resolve linked project: %w", err)
	}
	// Validate and hash the same canonical target we link, rather than
	// resolving the caller's alias a second time after the containment check.
	dir, err := prepare(stateRoot, workspaceRoot, user, target, legacyRel, session, provider, mode)
	if err != nil {
		return "", err
	}
	link := filepath.Join(dir, ProjectLink)
	if err := os.Symlink(target, link); err != nil && !os.IsExist(err) {
		return "", fmt.Errorf("link project into CLI runtime: %w", err)
	}
	// Never overwrite an obstruction, repoint a link, or adopt a user-created
	// directory. A changed link fails the next launch instead of widening access.
	saved, err := os.Readlink(link)
	if err == nil && saved != target && legacyRel != "" {
		if oldTarget, ok := legacyProjectTarget(workspaceRoot, legacyRel); ok && saved == oldTarget {
			if err := repointProjectLink(link, target); err != nil {
				return "", fmt.Errorf("repoint the moved project's CLI runtime link: %w", err)
			}
			saved = target
		}
	}
	if err != nil || saved != target {
		return "", fmt.Errorf("CLI runtime project link does not match its authorized project")
	}
	return dir, nil
}

// legacyProjectTarget is the link target a runtime made for the project at its old workspace-relative path held.
func legacyProjectTarget(workspaceRoot, legacyRel string) (string, bool) {
	resolved, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return "", false
	}
	return filepath.Join(resolved, filepath.FromSlash(legacyRel)), true
}

// repointProjectLink replaces the symlink at link with one to target, atomically (a new link beside it, renamed over).
func repointProjectLink(link, target string) error {
	tmp := fmt.Sprintf("%s.repoint-%d", link, os.Getpid())
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// RepointProjectLinks rewrites the project link of every runtime under stateRoot/cli-runtimes/v1 whose link points at
// oldTarget so it points at newTarget, and returns how many it changed. The Crew move calls it after a Crew has moved
// (a runtime's link target is the project's absolute path); a runtime with any other link is left alone.
func RepointProjectLinks(stateRoot, oldTarget, newTarget string) (int, error) {
	base := filepath.Join(stateRoot, "cli-runtimes", "v1")
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	changed := 0
	for _, entry := range entries {
		link := filepath.Join(base, entry.Name(), ProjectLink)
		saved, err := os.Readlink(link)
		if err != nil || saved != oldTarget {
			continue
		}
		if err := repointProjectLink(link, newTarget); err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}

// ProjectLinksTo lists the runtime folders under stateRoot whose project link points at target.
func ProjectLinksTo(stateRoot, target string) ([]string, error) {
	base := filepath.Join(stateRoot, "cli-runtimes", "v1")
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var dirs []string
	for _, entry := range entries {
		dir := filepath.Join(base, entry.Name())
		if saved, err := os.Readlink(filepath.Join(dir, ProjectLink)); err == nil && saved == target {
			dirs = append(dirs, dir)
		}
	}
	return dirs, nil
}
