package localfiles

import (
	"context"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"os"
	"path/filepath"
	"testing"
)

func TestExecutorLocalGrantsCannotBeWidenedByServer(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "files")
	os.Mkdir(root, 0700)
	os.WriteFile(filepath.Join(root, "readme.md"), []byte("local"), 0600)
	e, err := Open("laptop", []Grant{{Resource: Resource{ID: "project", Guard: wf.FolderGuard{ReadPaths: []string{"."}}}, Root: root, State: filepath.Join(base, "state")}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if _, err := os.Stat(filepath.Join(base, "state")); !os.IsNotExist(err) {
		t.Fatalf("read-only connection created writable state: %v", err)
	}
	r := Request{ID: "one", ResourceID: "project", Operation: "read", Path: "readme.md"}
	result := e.Execute(context.Background(), r)
	if result.Status != 200 || result.File.Content != "local" {
		t.Fatalf("read %+v", result)
	}
	r.Operation = "write"
	r.Content = "changed"
	r.ExpectedRevision = result.File.Revision
	r.RequestID = "one"
	if got := e.Execute(context.Background(), r); got.Status != 403 {
		t.Fatalf("read-only %+v", got)
	}
	r.Operation = "read"
	r.Path = "../outside"
	if got := e.Execute(context.Background(), r); got.Status != 400 {
		t.Fatalf("traversal %+v", got)
	}
	r.ResourceID = "unshared"
	if got := e.Execute(context.Background(), r); got.Status != 404 {
		t.Fatalf("unshared %+v", got)
	}
	r.ResourceID = "project"
	r.Operation = "execute_shell"
	if got := e.Execute(context.Background(), r); got.Status != 400 {
		t.Fatalf("shell %+v", got)
	}
	r.Operation = "list"
	r.Path = "."
	if got := e.Execute(context.Background(), r); got.Status != 200 || len(got.Entries) != 1 {
		t.Fatalf("list %+v", got)
	}
}
