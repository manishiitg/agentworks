package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func crewAttachmentTestOrchestrator(t *testing.T, manifest string) *BaseOrchestrator {
	t.Helper()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	dir := filepath.Join(root, "Workflow", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	// Reads re-validate the attachment root on every access, so the
	// fixture crew workspace must exist for the alias to resolve.
	if err := os.MkdirAll(filepath.Join(root, "_users", "owner", "Chats", "Work", "projects", "project-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &BaseOrchestrator{workspacePath: "Workflow/demo", logger: loggerv2.NewNoop()}
}

const crewAttachmentTestManifest = `{"id":"wf-1","crew_attachments":[{"id":"a1","alias":"project-a","crew_profile_id":"work","crew_project_id":"project-a","crew_workspace_path":"_users/owner/Chats/Work/projects/project-a"}]}`

func TestResolveWorkspacePathMapsCrewAttachmentAlias(t *testing.T) {
	bo := crewAttachmentTestOrchestrator(t, crewAttachmentTestManifest)
	if got := bo.resolveWorkspacePath("project-a/reports/pr-87.md"); got != "_users/owner/Chats/Work/projects/project-a/reports/pr-87.md" {
		t.Fatalf("alias path = %q", got)
	}
	if got := bo.resolveWorkspacePath("runs/iteration-0/execution/out.md"); got != "Workflow/demo/runs/iteration-0/execution/out.md" {
		t.Fatalf("ordinary path = %q", got)
	}
	if got := bo.resolveWorkspacePath("project-a/../../escape.md"); strings.HasPrefix(got, "_users/") {
		t.Fatalf("escape resolved into crew root: %q", got)
	}
}

func TestCrewAttachmentReadFailsClosedAfterDetach(t *testing.T) {
	bo := crewAttachmentTestOrchestrator(t, crewAttachmentTestManifest)
	if got := bo.resolveWorkspacePath("project-a/reports/pr-87.md"); got != "_users/owner/Chats/Work/projects/project-a/reports/pr-87.md" {
		t.Fatalf("attached path = %q", got)
	}
	// Detach between reads: the same alias must stop resolving without
	// waiting for a new run or session.
	root := os.Getenv("WORKSPACE_DOCS_PATH")
	if err := os.WriteFile(filepath.Join(root, "Workflow", "demo", "workflow.json"), []byte(`{"id":"wf-1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := bo.resolveWorkspacePath("project-a/reports/pr-87.md"); got != "Workflow/demo/project-a/reports/pr-87.md" {
		t.Fatalf("detached path = %q, want workflow-local fallback", got)
	}
}

func TestCrewAttachmentReadFailsClosedOnRetargetOrDelete(t *testing.T) {
	retargeted := `{"id":"wf-1","crew_attachments":[{"id":"a1","alias":"project-a","crew_profile_id":"work","crew_project_id":"project-a","crew_workspace_path":"_users/other/Chats/Work/projects/evil"}]}`
	bo := crewAttachmentTestOrchestrator(t, retargeted)
	if got := bo.resolveWorkspacePath("project-a/notes.md"); strings.HasPrefix(got, "_users/") {
		t.Fatalf("retargeted path resolved into crew root: %q", got)
	}
	deleted := `{"id":"wf-1","crew_attachments":[{"id":"a1","alias":"project-a","crew_profile_id":"work","crew_project_id":"gone","crew_workspace_path":"_users/owner/Chats/Work/projects/gone"}]}`
	bo = crewAttachmentTestOrchestrator(t, deleted)
	if got := bo.resolveWorkspacePath("project-a/notes.md"); strings.HasPrefix(got, "_users/") {
		t.Fatalf("deleted-crew path resolved into crew root: %q", got)
	}
}

func TestCrewAttachmentReadsSkippedOutsideWorkflows(t *testing.T) {
	bo := &BaseOrchestrator{workspacePath: "Chats/general", logger: loggerv2.NewNoop()}
	if got := bo.resolveWorkspacePath("project-a/file.md"); got != "Chats/general/project-a/file.md" {
		t.Fatalf("non-workflow path = %q", got)
	}
}

func TestCrewAttachmentMutationsBlocked(t *testing.T) {
	bo := crewAttachmentTestOrchestrator(t, crewAttachmentTestManifest)
	ctx := context.Background()
	if err := bo.WriteWorkspaceFile(ctx, "project-a/notes.md", "x"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("write err = %v", err)
	}
	if err := bo.DeleteWorkspaceFile(ctx, "project-a/notes.md"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("delete err = %v", err)
	}
	if err := bo.MoveWorkspaceFile(ctx, "project-a/a.md", "runs/b.md"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("move-from err = %v", err)
	}
	if err := bo.MoveWorkspaceFile(ctx, "runs/a.md", "project-a/b.md"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("move-to err = %v", err)
	}
	if err := bo.CleanupDirectory(ctx, "project-a", "crew"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("cleanup err = %v", err)
	}
}
