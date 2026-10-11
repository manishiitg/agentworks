package step_based_workflow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEveryWorkflowGuardMaterializesFreshWorkspacePaths(t *testing.T) {
	docsRoot := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docsRoot)

	newOrchestrator := func(t *testing.T) *StepBasedWorkflowOrchestrator {
		t.Helper()
		hcpo := newAgentFactoryTestOrchestrator(t)
		hcpo.SetWorkspacePath("Workflow/fresh-product")
		hcpo.selectedRunFolder = "iteration-0/default"
		return hcpo
	}

	cases := []struct {
		name  string
		paths func(*StepBasedWorkflowOrchestrator) ([]string, []string)
	}{
		{
			name: "execution",
			paths: func(hcpo *StepBasedWorkflowOrchestrator) ([]string, []string) {
				return hcpo.setupExecutionFolderGuard("step-1", "brief", KBAccessNone, LearningsAccessNone, DBAccessRead, nil)
			},
		},
		{
			name: "agent",
			paths: func(hcpo *StepBasedWorkflowOrchestrator) ([]string, []string) {
				return hcpo.setupAgentFolderGuard("step-1", "brief", nil, AgentWriteAccess{})
			},
		},
		{
			name: "kb_update",
			paths: func(hcpo *StepBasedWorkflowOrchestrator) ([]string, []string) {
				return hcpo.setupKBUpdateFolderGuard("brief", "step-1")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hcpo := newOrchestrator(t)
			readPaths, writePaths := tc.paths(hcpo)
			if err := hcpo.materializeWorkflowGuardPaths(readPaths, writePaths); err != nil {
				t.Fatal(err)
			}
			managed := append([]string{"Workflow/fresh-product/tool_output_folder"}, writePaths...)
			for _, path := range managed {
				info, err := os.Stat(filepath.Join(docsRoot, path))
				if err != nil || !info.IsDir() {
					t.Fatalf("managed guard path %q was not created: info=%v err=%v", path, info, err)
				}
			}
		})
	}
}

func TestWorkflowGuardMaterializationFailsClosedOutsideDocsRoot(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	hcpo := newAgentFactoryTestOrchestrator(t)
	hcpo.SetWorkspacePath("Workflow/fresh-product")
	if err := hcpo.materializeWorkflowGuardPaths(nil, []string{"../outside"}); err == nil {
		t.Fatal("escaping managed path unexpectedly succeeded")
	}
}

func TestWorkflowGuardMaterializationLeavesAbsoluteHostGrantUnmanaged(t *testing.T) {
	docsRoot := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docsRoot)
	hcpo := newAgentFactoryTestOrchestrator(t)
	hcpo.SetWorkspacePath("Workflow/fresh-product")
	hostRoot := t.TempDir()
	if err := hcpo.materializeWorkflowGuardPaths([]string{hostRoot}, []string{hostRoot}); err != nil {
		t.Fatalf("pre-existing host grant should not be treated as a workspace-managed path: %v", err)
	}
}
