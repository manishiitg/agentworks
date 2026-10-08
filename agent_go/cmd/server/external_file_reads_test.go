package server

import (
	"context"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func TestExternalFileReadsUseSharedFilesystemWithoutWorkflowFilesEndpoint(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	workflow := filepath.Join(docs, "Workflow", "invoices")
	if err := os.MkdirAll(filepath.Join(workflow, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workflow, "docs", "notes.md"), []byte("first line\nneedle here\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	read, err := externalFileRequest(ctx, wf.Request{Root: "Workflow/invoices", Operation: "read", Path: "docs/notes.md"})
	if err != nil || !read.Exists || read.Content != "first line\nneedle here\n" {
		t.Fatalf("direct read = %+v, %v", read, err)
	}
	listed, err := externalFileRequest(ctx, wf.Request{Root: "Workflow/invoices", Operation: "list", Path: "docs"})
	if err != nil || !listed.Exists || listed.Path != "docs" || len(listed.Entries) != 1 || listed.Entries[0].Path != "docs/notes.md" {
		t.Fatalf("direct list = %+v, %v", listed, err)
	}
	searched, err := externalFileRequest(ctx, wf.Request{Root: "Workflow/invoices", Operation: "search", Path: "docs", Query: "needle"})
	if err != nil || len(searched.Entries) != 1 || searched.Entries[0].Line != 2 {
		t.Fatalf("direct search = %+v, %v", searched, err)
	}
	for _, req := range []wf.Request{
		{Root: "Workflow/invoices", Operation: "read", Path: "../outside"},
		{Root: "Workflow/invoices", Operation: "read", Path: ".env"},
		{Root: "Workflow/invoices", Operation: "commit", Path: "docs/notes.md"},
	} {
		if _, err := externalFileRequest(ctx, req); err == nil {
			t.Fatalf("accepted invalid request %+v", req)
		}
	}
}

func TestExternalFileGuardsApplyBeforePaginationAndSearch(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	root := filepath.Join(docs, "Workflow", "one")
	for _, dir := range []string{"aaa-denied", "visible"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 101; i++ {
		if err := os.WriteFile(filepath.Join(root, "aaa-denied", fmt.Sprintf("%03d.txt", i)), []byte("needle"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"first.txt", "second.txt"} {
		if err := os.WriteFile(filepath.Join(root, "visible", name), []byte("needle"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{FileGuard: &wf.FolderGuard{ReadPaths: []string{"visible"}}}})
	for _, op := range []string{"list", "search"} {
		req := wf.Request{Root: "Workflow/one", Operation: op, Path: ".", Glob: "**/*.txt", Query: "needle", Limit: 1}
		first, err := externalFileRequest(ctx, req)
		if err != nil || len(first.Entries) != 1 || first.Entries[0].Path != "visible/first.txt" || first.NextOffset != 1 {
			t.Fatalf("%s first page: %+v %v", op, first, err)
		}
		req.Offset = first.NextOffset
		second, err := externalFileRequest(ctx, req)
		if err != nil || len(second.Entries) != 1 || second.Entries[0].Path != "visible/second.txt" {
			t.Fatalf("%s second page: %+v %v", op, second, err)
		}
	}
}

func TestExternalFileReadsRejectSymlinks(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	workflow := filepath.Join(docs, "Workflow", "invoices")
	if err := os.MkdirAll(workflow, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(docs, filepath.Join(workflow, "outside")); err != nil {
		t.Fatal(err)
	}
	_, err := externalFileRequest(context.Background(), wf.Request{Root: "Workflow/invoices", Operation: "read", Path: "outside/secret"})
	var upstream *externalUpstreamError
	if !errors.As(err, &upstream) || upstream.status != 403 || !strings.Contains(upstream.message, "symbolic") {
		t.Fatalf("symlink read error = %v", err)
	}
}

func TestExternalFileReadsFilterSourceAndProtectRuntimePaths(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	root := filepath.Join(docs, "Workflow", "invoices")
	for name, content := range map[string]string{
		"code/run-basic-smoke/main.py":         "def test_auth():\n    return True\n",
		"code/run-basic-smoke/modules/auth.py": "def test_login():\n    return True\n",
		"code/run-basic-smoke/README.md":       "test login guidance\n",
		"code/.local/lib/installed.py":         "def test_hidden():\n    return True\n",
		"code/.cache/wheel.py":                 "def test_hidden():\n    return True\n",
	} {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	req := wf.Request{Root: "Workflow/invoices", Operation: "list", Path: "code", Glob: "**/*.py", Depth: 8}
	listed, err := externalFileRequest(ctx, req)
	if err != nil || !listed.Exists || listed.Path != "code" || len(listed.Entries) != 2 {
		t.Fatalf("filtered list = %+v, %v", listed, err)
	}
	for _, entry := range listed.Entries {
		if !strings.HasPrefix(entry.Path, "code/run-basic-smoke/") {
			t.Errorf("unexpected path: %s", entry.Path)
		}
	}
	req.Operation, req.Query = "search", "test_login"
	searched, err := externalFileRequest(ctx, req)
	if err != nil || len(searched.Entries) != 1 || searched.Entries[0].Path != "code/run-basic-smoke/modules/auth.py" {
		t.Fatalf("filtered search = %+v, %v", searched, err)
	}
	for _, name := range []string{"code/.local/lib/installed.py", "code/.cache/wheel.py"} {
		if _, err := externalFileRequest(ctx, wf.Request{Root: "Workflow/invoices", Operation: "read", Path: name}); err == nil {
			t.Errorf("direct read allowed excluded path %s", name)
		}
	}
	if _, err := externalFileRequest(ctx, wf.Request{Root: "Workflow/invoices", Operation: "list", Path: "code", Glob: "../*.py"}); err == nil {
		t.Error("accepted invalid glob")
	}
}

func TestExternalFileReadsUseSharedAssetsWhenWorkspaceIsRemote(t *testing.T) {
	f := newExternalToolsFixture(t)
	f.write(t, "Workflow/invoices/docs/remote.md", "remote needle\n")
	f.write(t, "Workflow/invoices/code/run-basic-smoke/main.py", "def test_login(): pass\n")
	f.write(t, "Workflow/invoices/code/.cache/installed.py", "def test_hidden(): pass\n")
	ctx := context.Background()
	read, err := externalRemoteFileRequest(ctx, wf.Request{Root: "Workflow/invoices", Operation: "read", Path: "docs/remote.md"}, "docs/remote.md")
	if err != nil || !read.Exists || read.Content != "remote needle\n" {
		t.Fatalf("remote read = %+v, %v", read, err)
	}
	listed, err := externalRemoteFileRequest(ctx, wf.Request{Root: "Workflow/invoices", Operation: "list", Path: "docs"}, "docs")
	if err != nil || len(listed.Entries) != 2 {
		t.Fatalf("remote list = %+v, %v", listed, err)
	}
	searched, err := externalRemoteFileRequest(ctx, wf.Request{Root: "Workflow/invoices", Operation: "search", Path: "docs", Query: "needle"}, "docs")
	if err != nil || !searched.Exists || searched.Path != "docs" || len(searched.Entries) != 1 || searched.Entries[0].Path != "docs/remote.md" {
		t.Fatalf("remote search = %+v, %v", searched, err)
	}
	filtered, err := externalRemoteFileRequest(ctx, wf.Request{Root: "Workflow/invoices", Operation: "list", Path: "code", Glob: "**/*.py", Depth: 8}, "code")
	if err != nil || len(filtered.Entries) != 1 || filtered.Entries[0].Path != "code/run-basic-smoke/main.py" {
		t.Fatalf("remote filtered list = %+v, %v", filtered, err)
	}
	_, err = externalRemoteFileRequest(ctx, wf.Request{Root: "Workflow/invoices", Operation: "list", Path: "missing"}, "missing")
	var upstream *externalUpstreamError
	if !errors.As(err, &upstream) || upstream.status != 404 {
		t.Fatalf("missing remote directory = %v", err)
	}
}

// MCP feedback 2026-10-08: a plain search for "components" returned an empty
// stub with truncated:true, so the caller could not tell "no match" from "did
// not look". A search must find names as well as text, reach deep files by
// default, say what an empty result means, and keep depth-limited apart from a
// cap (truncated).
func TestExternalFileSearchFindsNamesAndDeepTextAndExplainsEmptyResults(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	root := filepath.Join(docs, "Workflow", "site")
	for name, content := range map[string]string{
		"src/components/Button.tsx": "export const Button = 1\n",
		"src/a/b/c/d/e/deep.txt":    "buried needle\n",
		"notes.md":                  "nothing here\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	byName, err := externalFileRequest(ctx, wf.Request{Root: "Workflow/site", Operation: "search", Path: ".", Query: "components"})
	if err != nil || len(byName.Entries) == 0 || !strings.Contains(byName.Entries[0].Path, "components") {
		t.Fatalf("search by name = %+v, %v", byName, err)
	}
	deep, err := externalFileRequest(ctx, wf.Request{Root: "Workflow/site", Operation: "search", Path: ".", Query: "needle"})
	if err != nil || len(deep.Entries) != 1 || deep.Entries[0].Line != 1 || deep.Truncated || deep.DepthLimited {
		t.Fatalf("a default search must reach a file six folders down without flags: %+v, %v", deep, err)
	}
	empty, err := externalFileRequest(ctx, wf.Request{Root: "Workflow/site", Operation: "search", Path: ".", Query: "absent-term"})
	if err != nil || len(empty.Entries) != 0 || empty.Searched == 0 || !strings.Contains(empty.Note, "No file name or text") {
		t.Fatalf("an empty search must say what it searched: %+v, %v", empty, err)
	}
	shallow, err := externalFileRequest(ctx, wf.Request{Root: "Workflow/site", Operation: "search", Path: ".", Query: "needle", Depth: 2})
	if err != nil || len(shallow.Entries) != 0 || !shallow.DepthLimited || shallow.Truncated || !strings.Contains(shallow.Note, "depth 2") {
		t.Fatalf("a shallow search must report depth_limited, not truncated: %+v, %v", shallow, err)
	}
}
