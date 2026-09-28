package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func TestExternalBuilderManagedFileTools(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	root := filepath.Join(docs, "Workflow", "test")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	reg := &recordingRegistrar{}
	if err := api.registerExternalBuilderWorkspaceTools(reg, "Workflow/test", &UserClaims{ExternalBuilderOperationID: "op"}); err != nil {
		t.Fatal(err)
	}
	if len(reg.tools) != 4 {
		t.Fatalf("file tools missing: %+v", reg.tools)
	}
	call := func(name string, args map[string]interface{}) wf.Result {
		t.Helper()
		raw, err := reg.tools[name].exec(context.Background(), args)
		if err != nil {
			t.Fatal(err)
		}
		var result wf.Result
		if err = json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	created := call("write_file", map[string]interface{}{"path": "code/main.py", "content": "print(1)", "expected_revision": "missing"})
	if created.Revision != wf.Revision([]byte("print(1)")) {
		t.Fatal("missing revision", created)
	}
	read := call("read_file", map[string]interface{}{"path": "code/main.py"})
	if read.Content != "print(1)" || read.Revision != created.Revision {
		t.Fatal("wrong read", read)
	}
	call("write_file", map[string]interface{}{"path": "code/main.py", "content": "print(2)", "expected_revision": read.Revision})
	if _, err := reg.tools["write_file"].exec(context.Background(), map[string]interface{}{"path": "code/main.py", "content": "stale", "expected_revision": read.Revision}); err == nil {
		t.Fatal("stale overwrite accepted")
	}
	for _, name := range []string{"read_file", "write_file"} {
		for _, p := range []string{"../other/main.py", "/tmp/secret", "db/db.sqlite", "builder/owner/private.json", "secrets/token", ".hidden/secret"} {
			if _, err := reg.tools[name].exec(context.Background(), map[string]interface{}{"path": p, "content": "x", "expected_revision": "missing"}); err == nil {
				t.Fatalf("%s permitted private path %s", name, p)
			}
		}
	}
	for _, p := range []string{"workflow.json", "planning/plan.json", "planning/step_config.json"} {
		if _, err := reg.tools["write_file"].exec(context.Background(), map[string]interface{}{"path": p, "content": "x", "expected_revision": "missing"}); err == nil {
			t.Fatalf("raw mutation accepted: %s", p)
		}
	}
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "secret"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"read_file", "write_file"} {
		if _, err := reg.tools[name].exec(context.Background(), map[string]interface{}{"path": "link/secret", "content": "x", "expected_revision": wf.Revision([]byte("private"))}); err == nil {
			t.Fatal("symlink escaped scope", name)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "db"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "db", "db.sqlite"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	listed := call("list_files", map[string]interface{}{"path": "."})
	for _, entry := range listed.Entries {
		if entry.Path == "db" || entry.Path == "db/db.sqlite" || entry.Path == "link" {
			t.Fatal("private list entry", entry)
		}
	}
}
