package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func TestExternalBuilderManagedFileTools(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	t.Setenv("AUTH_SECRET", "builder-managed-file-test-secret")
	root := filepath.Join(docs, "Workflow", "test")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	reg := &recordingRegistrar{}
	claims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{ID: "grant"}, ExternalBuilderOperationID: "op"}
	ctx := context.WithValue(context.Background(), UserContextKey, claims)
	if err := api.registerExternalBuilderWorkspaceTools(reg, "Workflow/test", claims); err != nil {
		t.Fatal(err)
	}
	if len(reg.tools) != 4 {
		t.Fatalf("file tools missing: %+v", reg.tools)
	}
	call := func(name string, args map[string]interface{}) wf.Result {
		t.Helper()
		raw, err := reg.tools[name].exec(ctx, args)
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
	if err := os.Chmod(filepath.Join(root, "code", "main.py"), 0640); err != nil {
		t.Fatal(err)
	}
	call("write_file", map[string]interface{}{"path": "code/main.py", "content": "print(2)", "expected_revision": read.Revision})
	if stat, err := os.Stat(filepath.Join(root, "code", "main.py")); err != nil || stat.Mode().Perm() != 0640 {
		t.Fatalf("file mode changed on replacement: %v %v", stat, err)
	}
	if _, err := reg.tools["write_file"].exec(ctx, map[string]interface{}{"path": "code/main.py", "content": "stale", "expected_revision": read.Revision}); err == nil {
		t.Fatal("stale overwrite accepted")
	}
	edits, err := listExternalBuilderFileEdits(ctx, claims, "test", "Workflow/test", "code/main.py")
	if err != nil || len(edits) != 2 || edits[0].Status != "completed" || edits[0].BeforeRevision != read.Revision || edits[0].AfterRevision != wf.Revision([]byte("print(2)")) {
		t.Fatalf("missing durable file versions: %+v %v", edits, err)
	}
	otherGrant := &UserClaims{UserID: claims.UserID, AccessToken: &accesstokens.Token{ID: "other"}}
	if recovered, err := listExternalBuilderFileEdits(ctx, otherGrant, "test", "Workflow/test", "code/main.py"); err != nil || len(recovered) != 2 || recovered[0].ViaToken != "token:grant" {
		t.Fatalf("same account could not recover old grant's file versions: %+v %v", recovered, err)
	}
	foreignUser := &UserClaims{UserID: "other", AccessToken: &accesstokens.Token{ID: "grant"}}
	if foreign, err := listExternalBuilderFileEdits(ctx, foreignUser, "test", "Workflow/test", "code/main.py"); err != nil || len(foreign) != 0 {
		t.Fatalf("another account saw file versions: %+v %v", foreign, err)
	}
	if _, err := readExternalBuilderFileEdit(ctx, foreignUser, "test", "Workflow/test", "code/main.py", edits[0].ID); err == nil {
		t.Fatal("another account restored a file version")
	}
	workflow := DiscoveredWorkflow{WorkspacePath: "Workflow/test", Manifest: &WorkflowManifest{ID: "test"}}
	restore := func(editID, expected string) wf.Result {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/external/v1/call", nil).WithContext(ctx)
		api.externalBuilderFileCall(w, r, "builder_restore_file", map[string]interface{}{"path": "code/main.py", "edit_id": editID, "expected_revision": expected}, workflow)
		if w.Code != http.StatusOK {
			t.Fatalf("restore failed: %d %s", w.Code, w.Body)
		}
		var out struct {
			Restored wf.Result `json:"restored"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Restored
	}
	restored := restore(edits[0].ID, wf.Revision([]byte("print(2)")))
	if restored.Revision != read.Revision || restored.Content != "print(1)" {
		t.Fatalf("restore did not recover previous text: %+v", restored)
	}
	if stat, err := os.Stat(filepath.Join(root, "code", "main.py")); err != nil || stat.Mode().Perm() != 0640 {
		t.Fatalf("restore changed file mode: %v %v", stat, err)
	}
	removed := restore(edits[1].ID, read.Revision)
	if removed.Exists || removed.Revision != wf.MissingRevision {
		t.Fatalf("restore of creation did not remove file: %+v", removed)
	}
	for _, name := range []string{"read_file", "write_file"} {
		for _, p := range []string{"../other/main.py", "/tmp/secret", "db/db.sqlite", "builder/owner/private.json", "secrets/token", ".hidden/secret"} {
			if _, err := reg.tools[name].exec(ctx, map[string]interface{}{"path": p, "content": "x", "expected_revision": "missing"}); err == nil {
				t.Fatalf("%s permitted private path %s", name, p)
			}
		}
	}
	for _, p := range []string{"workflow.json", "planning/plan.json", "planning/step_config.json"} {
		if _, err := reg.tools["write_file"].exec(ctx, map[string]interface{}{"path": p, "content": "x", "expected_revision": "missing"}); err == nil {
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
		if _, err := reg.tools[name].exec(ctx, map[string]interface{}{"path": "link/secret", "content": "x", "expected_revision": wf.Revision([]byte("private"))}); err == nil {
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
