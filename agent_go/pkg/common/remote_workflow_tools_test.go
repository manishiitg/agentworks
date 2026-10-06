package common

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/remoteplacement"
)

func TestRemoteWorkflowToolsModeUsesLogicalScope(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	placement := filepath.Join(t.TempDir(), "placements.json")
	t.Setenv(remoteplacement.FileEnv, placement)
	if err := os.WriteFile(placement, []byte(`{"workflows":{"Workflow/remote":"team"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, workspace, workflow, workingDir string
		remote                                bool
	}{
		{"relative workflow", "Workflow/remote", "", "", true},
		{"absolute nested workflow", filepath.Join(root, "Workflow/remote/runs/1"), "", "", true},
		{"private CLI runtime", filepath.Join(t.TempDir(), "runtime"), "Workflow/remote", "", true},
		{"scratch runtime", filepath.Join(root, "_system/remote-scratch/team/remote"), "", "Workflow/remote/runs/1", true},
		{"local workflow", "Workflow/local", "Workflow/local", "Workflow/local", false},
		{"local Crew", "_users/alice/Chats/Work/projects/site", "", "_users/alice/Chats/Work/projects/site", false},
		{"local Vault", "_users/alice/Chats/Vault", "", "_users/alice/Chats/Vault", false},
		{"prefix sibling", "Workflow/remote-copy", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := "remote-tools-" + tc.name
			t.Cleanup(func() { ClearSessionShellConfig(session) })
			SetSessionWorkflowPath(session, tc.workflow)
			SetSessionWorkingDir(session, tc.workingDir)
			// A local Crew may have read access to remote workflow files; that
			// attachment must not turn its own native tools off.
			SetSessionFolderGuard(session, []string{"Workflow/remote"}, nil)
			for _, requested := range []string{"", "mcp_only", "full", "hybrid", "full_unconfined"} {
				want := requested
				if tc.remote {
					want = "mcp_only"
				}
				if got := EnforceRemoteWorkflowToolsMode(session, tc.workspace, requested); got != want {
					t.Fatalf("requested %q: got %q, want %q", requested, got, want)
				}
			}
		})
	}
}
