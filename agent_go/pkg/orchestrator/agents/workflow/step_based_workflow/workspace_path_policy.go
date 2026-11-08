package step_based_workflow

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspacepathpolicy"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
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
	if stateRoot := hcpo.pythonHelperStateRoot(); stateRoot != "" {
		// Slot directories inherit their slot group and setgid bit. Read-only
		// helper state needs group traversal too; 0700 made answer_file unreadable.
		// Open through the same anchored path resolver, never chmod a symlink.
		relative := strings.TrimPrefix(stateRoot, hcpo.GetWorkspacePath()+"/")
		root, path, err := workflowfiles.OpenWorkflowDirectory(docsRoot, hcpo.GetWorkspacePath(), relative)
		if err != nil {
			return err
		}
		defer root.Close()
		helper, err := workflowfiles.OpenDirectory(root, path, false)
		if err != nil {
			return err
		}
		defer helper.Close()
		if err := workflowfiles.ProtectServerTree(helper); err != nil {
			return err
		}
	}
	return nil
}
