package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// Owning a workflow is not enough to attach any folder: a non-admin may only
// attach folders inside their assigned roots; unchanged grants stay.
func TestWorkflowFolderGrantsNeedAssignedRoots(t *testing.T) {
	assigned := t.TempDir()
	inside := filepath.Join(assigned, "project")
	outside := t.TempDir()
	for _, dir := range []string{inside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	grant := func(path string) workflowtypes.WorkflowFolderGrant {
		return workflowtypes.WorkflowFolderGrant{ID: "g", Alias: "proj", Path: path, Access: workflowtypes.FolderAccessReadWrite}
	}
	if err := checkWorkflowFolderGrants(nil, []workflowtypes.WorkflowFolderGrant{grant(inside)}, []string{assigned}, false); err != nil {
		t.Fatalf("a folder inside the assigned root was refused: %v", err)
	}
	if err := checkWorkflowFolderGrants(nil, []workflowtypes.WorkflowFolderGrant{grant(outside)}, []string{assigned}, false); err == nil {
		t.Fatal("a member attached a folder outside their assigned roots")
	}
	if err := checkWorkflowFolderGrants(nil, []workflowtypes.WorkflowFolderGrant{grant(outside)}, nil, false); err == nil {
		t.Fatal("a member with no assigned roots attached a folder")
	}
	if err := checkWorkflowFolderGrants(nil, []workflowtypes.WorkflowFolderGrant{grant(outside)}, nil, true); err != nil {
		t.Fatalf("an admin was refused: %v", err)
	}
	existing := []workflowtypes.WorkflowFolderGrant{grant(outside)}
	if err := checkWorkflowFolderGrants(existing, existing, nil, false); err != nil {
		t.Fatalf("an unchanged existing grant was refused: %v", err)
	}
}
