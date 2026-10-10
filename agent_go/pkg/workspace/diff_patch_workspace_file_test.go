package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func TestStripWorkspacePrefixUsesEnvRoot(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", "/Users/mipl/ai-work/coding-agent-loop/workspace-docs")

	got := stripWorkspacePrefix("/Users/mipl/ai-work/coding-agent-loop/workspace-docs/Workflow/demo/learnings/_global/SKILL.md")
	want := "Workflow/demo/learnings/_global/SKILL.md"
	if got != want {
		t.Fatalf("stripWorkspacePrefix() = %q, want %q", got, want)
	}
}

func TestResolveLinkedFolderPathUsesTrustedSessionAlias(t *testing.T) {
	sessionID := "linked-folder-test"
	root := t.TempDir()
	common.SetSessionFolderGuard(sessionID, []string{root}, []string{root})
	common.SetSessionShellEnv(sessionID, map[string]string{"WORKFLOW_FOLDER_project_source": root})
	t.Cleanup(func() { common.ClearSessionShellConfig(sessionID) })

	client := NewClient("http://unused")
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	got := client.resolveLinkedFolderPath(ctx, "linked://docs-source/docs/readme.md")
	want := filepath.Join(root, "docs", "readme.md")
	if got != want {
		t.Fatalf("resolved path = %q, want %q", got, want)
	}
	if escaped := client.resolveLinkedFolderPath(ctx, "linked://docs-source/../secret"); escaped != "linked://docs-source/../secret" {
		t.Fatalf("traversal alias unexpectedly resolved to %q", escaped)
	}
}

func TestResolveLinkedFolderPathFallsBackToWorkFolderEnv(t *testing.T) {
	sessionID := "linked-folder-work-test"
	root := t.TempDir()
	common.SetSessionFolderGuard(sessionID, []string{root}, []string{root})
	common.SetSessionShellEnv(sessionID, map[string]string{"WORK_FOLDER_SITE": root})
	t.Cleanup(func() { common.ClearSessionShellConfig(sessionID) })

	client := NewClient("http://unused")
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	got := client.resolveLinkedFolderPath(ctx, "linked://site/index.html")
	want := filepath.Join(root, "index.html")
	if got != want {
		t.Fatalf("resolved path = %q, want %q", got, want)
	}
}

func TestAbsoluteDottedDirectoryGrantAllowsChildPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "source.v2")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	guard := &FolderGuardConfig{Enabled: true, ReadPaths: []string{root}, WritePaths: []string{root}}
	if err := validatePathAgainstGuard(guard, filepath.Join(root, "notes.md"), true); err != nil {
		t.Fatalf("dotted directory was misclassified as an exact file grant: %v", err)
	}
}

func TestStripWorkspacePrefixDiscoversSiblingWorkspaceDocs(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", "")
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent_go")
	docsDir := filepath.Join(root, "workspace-docs")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(agentDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldCwd)
	})
	actualAgentDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	actualDocsDir := filepath.Join(filepath.Dir(actualAgentDir), "workspace-docs")

	got := stripWorkspacePrefix(filepath.Join(actualDocsDir, "Workflow/demo/learnings/_global/SKILL.md"))
	want := "Workflow/demo/learnings/_global/SKILL.md"
	if got != want {
		t.Fatalf("stripWorkspacePrefix() = %q, want %q", got, want)
	}
}

func TestEncodeWorkspaceDocumentPath(t *testing.T) {
	got := encodeWorkspaceDocumentPath("Workflow/my flow/learnings/file #1.md")
	want := "Workflow/my%20flow/learnings/file%20%231.md"
	if got != want {
		t.Fatalf("encoded path = %q, want %q", got, want)
	}
}

func TestDiffPatchWorkspaceFileValidatesInternally(t *testing.T) {
	client := NewClient("http://127.0.0.1:1", WithFolderGuard(&FolderGuardConfig{
		Enabled:    true,
		WritePaths: []string{"Workflow/allowed"},
	}))

	_, err := client.DiffPatchWorkspaceFile(context.Background(), DiffPatchWorkspaceFileParams{
		Filepath: "Workflow/blocked/report.html",
		Diff:     "--- a/report.html\n+++ b/report.html\n@@ -0,0 +1 @@\n+hello\n",
	})
	if err == nil || !strings.Contains(err.Error(), "ACCESS DENIED") {
		t.Fatalf("diff patch should be denied before making an HTTP request, got %v", err)
	}
}

func TestResolveGuardRelativeWorkspacePathUsesSoleProjectRoot(t *testing.T) {
	client := NewClient("http://unused", WithFolderGuard(&FolderGuardConfig{
		Enabled:    true,
		WritePaths: []string{"_users/alice/Chats/Work/projects/demo"},
	}))
	got := client.resolveGuardRelativeWorkspacePath(context.Background(), "MEMORY.md")
	want := "_users/alice/Chats/Work/projects/demo/MEMORY.md"
	if got != want {
		t.Fatalf("relative project file = %q, want %q", got, want)
	}
}

func TestResolveGuardRelativeWorkspacePathLeavesAmbiguousRootsUnchanged(t *testing.T) {
	client := NewClient("http://unused", WithFolderGuard(&FolderGuardConfig{
		Enabled:    true,
		WritePaths: []string{"Workflow/one", "Workflow/two"},
	}))
	if got := client.resolveGuardRelativeWorkspacePath(context.Background(), "MEMORY.md"); got != "MEMORY.md" {
		t.Fatalf("ambiguous relative project file resolved to %q", got)
	}
}
