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
	target, err := filepath.EvalSymlinks(project)
	if err != nil {
		return "", fmt.Errorf("resolve linked project: %w", err)
	}
	// Validate and hash the same canonical target we link, rather than
	// resolving the caller's alias a second time after the containment check.
	dir, err := Prepare(stateRoot, workspaceRoot, user, target, session, provider, mode)
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
	if err != nil || saved != target {
		return "", fmt.Errorf("CLI runtime project link does not match its authorized project")
	}
	return dir, nil
}
