package step_based_workflow

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEveryWorkflowGuardMaterializesFreshWorkspacePaths(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(fmt.Sprint(bound), func(t *testing.T) {
			docsRoot := t.TempDir()
			t.Setenv("WORKSPACE_DOCS_PATH", docsRoot)
			t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
			runtime := filepath.Join(docsRoot, "Workflow/fresh-product")
			if bound {
				state := os.Getenv("AGENTWORKS_STATE_ROOT")
				os.MkdirAll(filepath.Join(state, "ownership/crew-executables"), 0700)
				os.MkdirAll(filepath.Join(docsRoot, "Crew/example"), 0700)
				runtime = filepath.Join(docsRoot, "_system/crew-data/crew/workflows/sequence")
				os.MkdirAll(runtime, 0700)
				raw, _ := json.Marshal(map[string]any{"projects": map[string]any{"work/example": map[string]string{"product": "work", "folder": "example", "project_id": "crew", "owner_id": "owner"}}})
				os.WriteFile(filepath.Join(state, "ownership/projects.json"), raw, 0600)
				raw, _ = json.Marshal(map[string]string{"id": "sequence", "crew_id": "crew", "crew_root": "Crew/example", "workspace_path": "Workflow/fresh-product", "kind": "workflow"})
				key := fmt.Sprintf("%x.json", sha256.Sum256([]byte("Workflow/fresh-product")))
				os.WriteFile(filepath.Join(state, "ownership/crew-executables", key), raw, 0600)
			}

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
						return hcpo.setupExecutionFolderGuard("step-1", "brief", KBAccessNone, DBAccessRead, nil)
					},
				},
				{
					name: "agent",
					paths: func(hcpo *StepBasedWorkflowOrchestrator) ([]string, []string) {
						return hcpo.setupAgentFolderGuard("step-1", "brief", nil, AgentWriteAccess{})
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
						info, err := os.Stat(filepath.Join(runtime, strings.TrimPrefix(path, "Workflow/fresh-product/")))
						if err != nil || !info.IsDir() {
							t.Fatalf("managed guard path %q was not created: info=%v err=%v", path, info, err)
						}
					}
				})
			}
			if bound {
				if _, err := os.Stat(filepath.Join(docsRoot, "Workflow/fresh-product")); !os.IsNotExist(err) {
					t.Fatal("step launch recreated retired workflow directory")
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

// A server slot could not traverse helpers/ to read reply.answer_file (PLAT-746).
// Keep helper state writable only by the service, with read/traverse for its slot group.
func TestPythonHelperStateIsReadableButNotWritableByItsSlotGroup(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	c := newAgentFactoryTestOrchestrator(t)
	c.SetWorkspacePath("Workflow/example")
	c.selectedRunFolder = "run-one"
	helper := filepath.Join(docs, c.pythonHelperStateRoot())
	if err := os.MkdirAll(helper, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(helper, os.ModeSetgid|0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(helper), os.ModeSetgid|0770); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(helper, "answers", "old.txt")
	if err := os.MkdirAll(filepath.Dir(old), 0770); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(old), os.ModeSetgid|0770); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("old"), 0660); err != nil {
		t.Fatal(err)
	}
	if err := c.materializeWorkflowGuardPaths(nil, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(helper)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSetgid == 0 || info.Mode().Perm() != 0750 {
		t.Fatalf("slot helper mode = %v; want setgid 0750", info.Mode())
	}
	if err := c.writePythonHelperState(context.Background(), c.pythonHelperStateRoot()+"/receipts/nested/new.json", `{}`); err != nil {
		t.Fatal(err)
	}
	log, err := c.openPythonHelperLog("slot-access")
	if err != nil {
		t.Fatal(err)
	}
	log.Close()
	for _, path := range []string{helper, filepath.Dir(old), filepath.Join(helper, "receipts"), filepath.Join(helper, "receipts/nested"), filepath.Dir(log.Path())} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0750 || info.Mode()&os.ModeSetgid == 0 {
			t.Fatalf("server helper directory mode %s: %v %v", path, info, err)
		}
	}
	for _, path := range []string{old, filepath.Join(helper, "receipts/nested/new.json"), log.Path()} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0640 {
			t.Fatalf("server helper file mode %s: %v %v", path, info, err)
		}
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
