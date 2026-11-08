package step_based_workflow

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspacepathpolicy"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// materializeWorkflowGuardPaths is the single filesystem-lifecycle boundary
// for workflow Folder Guards. Write grants are platform-owned directories, as
// is mcpagent's read-only tool-output spill directory. Creating them here means
// a fresh project behaves like an old project that happened to exercise those
// paths already, across every product that uses the workflow runtime.
func (hcpo *StepBasedWorkflowOrchestrator) materializeWorkflowGuardPaths(readPaths, writePaths []string) error {
	docsRoot := strings.TrimSpace(GetPromptDocsRoot())
	if docsRoot == "" {
		return fmt.Errorf("materialize workflow Folder Guard: workspace docs root is empty")
	}

	grants := make([]workspacepathpolicy.Grant, 0, len(writePaths)+1)
	seen := make(map[string]struct{}, len(writePaths)+1)
	addManagedDirectory := func(path string) error {
		path = filepath.Clean(strings.TrimSpace(path))
		// Absolute roots are user-attached, host-owned folders. They are already
		// existence/canonicalization checked when folder_access is resolved and
		// must never be created or forced beneath workspace-docs.
		if path == "" || path == "." || filepath.IsAbs(path) {
			return nil
		}
		if _, ok := seen[path]; ok {
			return nil
		}
		physical, err := fsutil.WorkspacePath(docsRoot, filepath.ToSlash(path))
		if err != nil {
			return err
		}
		// A logical source grant can resolve into a person's attached project.
		// Keep it host-owned, just like an explicit absolute host grant.
		if !crewroot.Within(docsRoot, physical) {
			return nil
		}
		seen[path] = struct{}{}
		grants = append(grants, workspacepathpolicy.Grant{
			Path:      physical,
			Lifecycle: workspacepathpolicy.PlatformManaged,
			Kind:      workspacepathpolicy.Directory,
			Mode:      0o700,
		})
		return nil
	}

	for _, path := range writePaths {
		if err := addManagedDirectory(path); err != nil {
			return err
		}
	}
	if stateRoot := hcpo.pythonHelperStateRoot(); stateRoot != "" {
		if err := addManagedDirectory(stateRoot); err != nil {
			return err
		}
	}
	toolOutputPath := filepath.Join(hcpo.GetWorkspacePath(), "tool_output_folder")
	for _, path := range readPaths {
		if filepath.Clean(strings.TrimSpace(path)) == toolOutputPath {
			if err := addManagedDirectory(path); err != nil {
				return err
			}
			break
		}
	}

	if _, err := workspacepathpolicy.Materialize(docsRoot, grants); err != nil {
		return fmt.Errorf("materialize workflow Folder Guard paths: %w", err)
	}
	return nil
}
