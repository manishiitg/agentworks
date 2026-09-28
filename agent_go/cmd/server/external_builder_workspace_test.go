package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestExternalBuilderAuditCapsContentAndHistory(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	t.Setenv("AUTH_SECRET", "builder-audit-cap-test-secret")
	ctx := context.Background()
	claims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{ID: "grant"}}
	large := strings.Repeat("x", externalBuilderAuditContentCap+1)
	id, err := prepareExternalBuilderEdit(ctx, claims, "op", "test", "Workflow/test", "write_file", "code/large.py", "old", "new", large, large, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = finishExternalBuilderEdit(id, "completed", ""); err != nil {
		t.Fatal(err)
	}
	edits, err := listExternalBuilderFileEdits(ctx, claims, "test", "Workflow/test", "code/large.py")
	if err != nil || len(edits) != 1 || edits[0].Restorable || edits[0].BeforeSize != int64(len(large)) || edits[0].AfterSize != int64(len(large)) {
		t.Fatalf("oversized edit not marked unavailable: %+v %v", edits, err)
	}
	store, err := openExternalBuilderStore()
	if err != nil {
		t.Fatal(err)
	}
	var beforeLength, afterLength int
	if err = store.db.QueryRow(`SELECT length(before_content),length(after_content) FROM external_builder_edits WHERE id=?`, id).Scan(&beforeLength, &afterLength); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if beforeLength != 0 || afterLength != 0 {
		t.Fatalf("file content persisted: before=%d after=%d", beforeLength, afterLength)
	}
	api := &StreamingAPI{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/external/v1/call", nil).WithContext(context.WithValue(ctx, UserContextKey, claims))
	api.externalBuilderFileCall(w, r, "builder_restore_file", map[string]interface{}{"path": "code/large.py", "edit_id": id, "expected_revision": "new"}, DiscoveredWorkflow{WorkspacePath: "Workflow/test", Manifest: &WorkflowManifest{ID: "test"}})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "restore_unavailable") {
		t.Fatalf("oversized restore: %d %s", w.Code, w.Body.String())
	}
	for i := 0; i < externalBuilderAuditHistoryLimit+2; i++ {
		if _, err = prepareExternalBuilderEdit(ctx, claims, "op", "test", "Workflow/test", "write_file", "code/main.py", "old", "new", "before", "after", true); err != nil {
			t.Fatal(err)
		}
	}
	store, err = openExternalBuilderStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var count int
	if err = store.db.QueryRow(`SELECT count(*) FROM external_builder_edits WHERE path='code/main.py'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != externalBuilderAuditHistoryLimit {
		t.Fatalf("retained %d edits, want %d", count, externalBuilderAuditHistoryLimit)
	}
	if _, err = store.db.Exec(`UPDATE external_builder_edits SET created_at=? WHERE id=?`, time.Now().Add(-externalBuilderAuditRetention-time.Hour).UnixNano(), id); err != nil {
		t.Fatal(err)
	}
	path, err := mcpOAuthSecret()
	if err != nil {
		t.Fatal(err)
	}
	externalBuilderAuditPruneState.Lock()
	delete(externalBuilderAuditPruneState.last, path)
	externalBuilderAuditPruneState.Unlock()
	if err = migrateAndPruneExternalBuilderEdits(store.db); err != nil {
		t.Fatal(err)
	}
	if err = store.db.QueryRow(`SELECT count(*) FROM external_builder_edits WHERE id=?`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expired edit remains: %d %v", count, err)
	}
}

func TestExternalBuilderAuditMigratesLegacyContents(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	t.Setenv("AUTH_SECRET", "builder-audit-legacy-test-secret")
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.db.Exec(`CREATE TABLE external_builder_edits (
 id TEXT PRIMARY KEY, operation_id TEXT NOT NULL, user_id TEXT NOT NULL, grant_id TEXT NOT NULL,
 workflow_id TEXT NOT NULL, workspace TEXT NOT NULL, tool TEXT NOT NULL, path TEXT NOT NULL,
 before_revision TEXT NOT NULL DEFAULT '', after_revision TEXT NOT NULL DEFAULT '',
 before_content TEXT NOT NULL DEFAULT '', after_content TEXT NOT NULL DEFAULT '',
 before_exists INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("x", externalBuilderAuditContentCap+1)
	for _, row := range []struct{ id, before string }{{"large", large}, {"small", "prior"}} {
		_, err = store.db.Exec(`INSERT INTO external_builder_edits
 (id,operation_id,user_id,grant_id,workflow_id,workspace,tool,path,before_content,after_content,before_exists,status,created_at)
 VALUES (?,'op','owner','grant','test','Workflow/test','write_file','code/main.py',?, 'result',1,'completed',?)`, row.id, row.before, time.Now().UnixNano())
		if err != nil {
			t.Fatal(err)
		}
	}
	store.Close()
	store, err = openExternalBuilderStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, row := range []struct {
		id                    string
		beforeSize, available int
		before                string
	}{
		{"large", len(large), 0, ""}, {"small", len("prior"), 1, "prior"},
	} {
		var before, after string
		var beforeSize, afterSize, available int
		if err = store.db.QueryRow(`SELECT before_content,after_content,before_size,after_size,before_content_available FROM external_builder_edits WHERE id=?`, row.id).Scan(&before, &after, &beforeSize, &afterSize, &available); err != nil {
			t.Fatal(err)
		}
		if before != row.before || after != "" || beforeSize != row.beforeSize || afterSize != len("result") || available != row.available {
			t.Fatalf("legacy %s not bounded: before=%d after=%d sizes=%d/%d available=%d", row.id, len(before), len(after), beforeSize, afterSize, available)
		}
	}
}
