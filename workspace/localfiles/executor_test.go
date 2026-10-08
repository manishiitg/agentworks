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
	if _, err := os.Stat(filepath.Join(base, "state")); err != nil {
		t.Fatalf("missing private command receipts: %v", err)
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

func TestExecutorRejectsBroadRootsAndPrivateCredentialsAcrossAliases(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	project := filepath.Join(home, "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	config := filepath.Join(project, "executor.json")
	if err := os.WriteFile(config, []byte("private tokens"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{string(filepath.Separator), base, home} {
		if e, err := Open("laptop", []Grant{{Resource: Resource{ID: "project"}, Root: root, State: filepath.Join(base, "state")}}); err == nil {
			e.Close()
			t.Fatalf("broad root accepted: %s", root)
		}
	}
	grants := []Grant{{Resource: Resource{ID: "project"}, Root: project, State: filepath.Join(base, "state")}, {Resource: Resource{ID: "reference"}, Root: t.TempDir(), State: filepath.Join(base, "state2"), PrivatePaths: []string{config}}}
	if e, err := Open("laptop", grants); err == nil {
		e.Close()
		t.Fatal("another alias exposed executor credentials")
	}
	grants = grants[:1]
	grants[0].PrivatePaths = []string{filepath.Join(base, "private-config.json")}
	if e, err := Open("laptop", grants); err != nil {
		t.Fatal(err)
	} else {
		e.Close()
	}
}

func TestDownloadsLinkRequiresIndependentWritableGrant(t *testing.T) {
	project := Resource{ID: "project", Downloads: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}}}
	companion := Resource{ID: "downloads", Writable: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}}}
	for _, resources := range [][]Resource{{project}, {project, {ID: "downloads"}}, {{ID: "downloads", Downloads: true, Writable: true}}} {
		if err := (Hello{Version: Version, DeviceID: "laptop", Resources: resources}).Validate(); err == nil {
			t.Fatalf("unapproved Downloads link accepted: %+v", resources)
		}
	}
	if err := (Hello{Version: Version, DeviceID: "laptop", Resources: []Resource{project, companion}}).Validate(); err != nil {
		t.Fatal(err)
	}
}
