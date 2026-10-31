package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

func crewLocalPathsOverlap(a, b string) bool {
	return crewroot.Within(a, b) || crewroot.Within(b, a)
}

func validateNewLocalCrewRoot(input string) (string, error) {
	if !IsLocalMode() {
		return "", fmt.Errorf("choosing an existing folder is available in the local app; create a managed Crew here")
	}
	input = strings.TrimSpace(input)
	if !filepath.IsAbs(input) {
		return "", fmt.Errorf("choose an existing folder using its full path")
	}
	root, err := filepath.EvalSymlinks(input)
	if err != nil {
		return "", fmt.Errorf("the selected folder is unavailable; choose an existing folder")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("choose an existing folder")
	}
	state, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	for _, managed := range []string{fsutil.WorkspaceDocsRoot(), state} {
		if real, e := filepath.EvalSymlinks(managed); e == nil {
			managed = real
		}
		if crewLocalPathsOverlap(root, managed) {
			return "", fmt.Errorf("choose a project folder separate from AgentWorks storage")
		}
	}
	if _, err := os.Lstat(filepath.Join(root, ".agentworks")); !os.IsNotExist(err) {
		return "", fmt.Errorf("this folder already contains .agentworks; choose a folder without an existing Crew")
	}
	return root, nil
}

func crewStorageRoots(folder string) (crewroot.Roots, bool, error) {
	return crewroot.Lookup(fsutil.WorkspaceDocsRoot(), folder)
}

func localCrewRoots(folder string) (crewroot.Roots, bool, error) {
	r, found, err := crewroot.Lookup(fsutil.WorkspaceDocsRoot(), folder)
	return r, found && r.Local, err
}

func localCrewRemovalRoots(folder string) (crewroot.Roots, bool, error) {
	r, found, err := crewroot.LookupForRemoval(fsutil.WorkspaceDocsRoot(), folder)
	return r, found && r.Local, err
}

func crewFilesPath(folder string) string {
	if roots, bound, err := crewStorageRoots(folder); bound && err == nil && roots.SeparateFiles() {
		return roots.FilesPath()
	}
	return folder
}
