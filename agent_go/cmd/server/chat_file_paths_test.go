package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

func TestChatFilePathsResolveOnlyCurrentVisibleFilesAndOwnAttachments(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENT_PRODUCTS", "work")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "work")
	withMemoryUserDirectory(t, `{"users":[
		{"id":"owner","username":"owner","products":["work"]},
		{"id":"reader","username":"reader","products":["work"]}
	]}`)
	const project = "Crew/file-links"
	checkout := t.TempDir()
	if err := defaultProjectOwners().Register(projectOwnerRecord{
		Product: "work", Folder: "file-links", OwnerID: "owner", ProjectID: "chat-file-links", Shared: true, LocalRoot: checkout,
	}); err != nil {
		t.Fatal(err)
	}
	resetCrewOwnerCache()
	t.Cleanup(resetCrewOwnerCache)
	other := registerTestCrew(t, "other-files", "owner", true)
	f.write(t, other+"/report.md", "other project")
	f.write(t, "_users/reader/Downloads/report.md", "private")
	write := func(root, relative string) string {
		t.Helper()
		p := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("example file"), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	report := write(checkout, ".agentworks/outputs/Daily report.md")
	code := write(checkout, "src/main.go")
	private := write(checkout, ".agentworks/builder/private.json")
	dotfile := write(checkout, ".env.local")
	grantedDir := t.TempDir()
	attached := write(grantedDir, "Design notes.md")
	unattached := write(t.TempDir(), "other.md")
	attachedSecret := write(grantedDir, ".config/credentials.json")
	escaped := filepath.Join(checkout, "escape.md")
	if err := os.Symlink(unattached, escaped); err != nil {
		t.Fatal(err)
	}

	oldStore := workFolderAccessStore
	doc := workFolderAccessDoc{
		Assignments: map[string]*workFolderAssignment{"owner": {Roots: []string{grantedDir}}},
		Grants: map[string][]workflowtypes.WorkflowFolderGrant{
			"owner": {{Alias: "design", Path: grantedDir, Access: "read_only"}},
		},
	}
	workFolderAccessStore = &workFolderStore{load: func(context.Context) (workFolderAccessDoc, error) { return doc, nil }}
	t.Cleanup(func() { workFolderAccessStore = oldStore })
	router := mux.NewRouter()
	registerWorkflowReadRoutes(router.PathPrefix("/api").Subrouter(), f.api)
	request := func(claims *UserClaims, workspace string, candidates ...string) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"workspace_path": workspace, "paths": candidates})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, adminRequest(http.MethodPost, "/api/chat-file-paths", string(raw), claims, nil))
		return w
	}

	owner := &UserClaims{UserID: "owner", Username: "owner"}
	response := request(owner, project,
		report, code, "src/main.go", project+"/_files/src/main.go", "outputs/Daily report.md", attached,
		"missing.md", "main.go", "src", escaped, private, dotfile, attachedSecret, unattached,
		other+"/report.md", filepath.Join(f.docs, other, "report.md"), filepath.Join(f.docs, "_users/reader/Downloads/report.md"),
		checkout+"/src/../src/main.go", checkout+"-other/src/main.go")
	var got map[string]chatFileTarget
	want := map[string]chatFileTarget{
		report:                          {Path: project + "/outputs/Daily report.md", Preview: true},
		"outputs/Daily report.md":       {Path: project + "/outputs/Daily report.md", Preview: true},
		code:                            {Path: project + "/_files/src/main.go"},
		"src/main.go":                   {Path: project + "/_files/src/main.go"},
		project + "/_files/src/main.go": {Path: project + "/_files/src/main.go"},
		attached:                        {Path: attached, Preview: true},
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &got) != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("availability response: %d %s; want %+v", response.Code, response.Body, want)
	}

	for _, claims := range []*UserClaims{nil,
		{UserID: "reader", Username: "reader"},
		{UserID: "owner", AccessToken: &accesstokens.Token{}},
		{UserID: "owner", Scope: "report-preview"},
		{UserID: "owner", Provider: "bot_owner"},
	} {
		if w := request(claims, project, code, attached); w.Code != http.StatusForbidden {
			t.Fatalf("caller outside Files access received metadata: %d %s", w.Code, w.Body)
		}
	}

	if _, err := os.Stat(filepath.Join(checkout, ".agentworks/settings")); !os.IsNotExist(err) {
		t.Fatalf("a metadata lookup provisioned Crew settings: %v", err)
	}
	readAttachment := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		f.api.handlePublicFile(w, adminRequest(http.MethodGet, sharedTestURL(attached), "", owner, nil))
		return w
	}
	if w := readAttachment(); w.Code != 200 || w.Body.String() != "example file" {
		t.Fatalf("existing file preview could not open the attachment: %d %s", w.Code, w.Body)
	}
	// A path lookup never grants future access: file removal and detaching
	// the folder are checked again on click and by the existing asset reader.
	delete(doc.Grants, "owner")
	if err := os.Remove(code); err != nil {
		t.Fatal(err)
	}
	if w := request(owner, project, code, attached); w.Code != 200 || w.Body.String() != "{}\n" {
		t.Fatalf("old path still reported available: %d %s", w.Code, w.Body)
	}
	if w := readAttachment(); w.Code != http.StatusForbidden {
		t.Fatalf("revoked folder still opened through existing preview: %d %s", w.Code, w.Body)
	}
}
