package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestWorkflowContextAccess(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":[]},{"id":"reader","username":"reader","can_create":false,"products":[]},{"id":"other","username":"other","can_create":true,"products":[]}]}`)
	manifest := WorkflowManifest{ID: "target", Label: "Target", Access: &WorkflowAccess{Owners: []string{"owner"}, Readers: []string{"reader"}}}
	raw, _ := json.Marshal(manifest)
	workspace := &mockWorkspaceAPI{files: map[string]string{manifestPath("Workflow/target"): string(raw)}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	ctxFor := func(id string) context.Context {
		return context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: id, Username: id})
	}
	for _, id := range []string{"owner", "reader"} {
		got, err := authorizeWorkflowContextPaths(ctxFor(id), []string{"Workflow/target", "Workflow/target/"})
		if err != nil || !reflect.DeepEqual(got, []string{"Workflow/target"}) {
			t.Fatalf("%s: paths=%v err=%v", id, got, err)
		}
		writes, reads := collectSplitFolderGuardFolders("", got)
		if len(writes) != 0 || !reflect.DeepEqual(reads, got) {
			t.Fatalf("context must grant only read access: writes=%v reads=%v", writes, reads)
		}
	}
	for _, paths := range [][]string{{"Workflow/target"}, {"Workflow/missing"}, {"Workflow"}, {"/Workflow/target"}, {"Workflow/target/../other"}, {"Workflow/target/planning"}, {"Chats/private"}, {"Workflow/.."}, {"Workflow/target", "Workflow/missing"}} {
		if got, err := authorizeWorkflowContextPaths(ctxFor("other"), paths); err == nil || got != nil {
			t.Fatalf("unauthorized request accepted: %v => %v %v", paths, got, err)
		}
	}
	workspace.files[userProductAccessFilePath()] = `{"owner":{"workflow_ids":["different-workflow"]}}`
	if _, err := authorizeWorkflowContextPaths(ctxFor("owner"), []string{"Workflow/target"}); err == nil {
		t.Fatal("workflow allowlist restriction was ignored")
	}
	delete(workspace.files, userProductAccessFilePath())
	// Sharing is checked again on the next request, not retained from a prior attachment.
	manifest.Access.Readers = nil
	raw, _ = json.Marshal(manifest)
	workspace.files[manifestPath("Workflow/target")] = string(raw)
	if _, err := authorizeWorkflowContextPaths(ctxFor("reader"), []string{"Workflow/target"}); err == nil {
		t.Fatal("revoked attachment was accepted")
	}
	// A crafted query fails before any session, agent or tool is initialized.
	recorder := httptest.NewRecorder()
	api := &StreamingAPI{}
	api.handleQuery(recorder, sharedSecretsRequest(http.MethodPost, "/api/query", "other", QueryRequest{Query: "read context", WorkflowContextPaths: []string{"Workflow/target"}}))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("query status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

// Crews are shared server-wide: another owner's Crew is reachable by its
// physical path; missing Crews and sub-folders are still rejected.
func TestCrewContextAccessIsServerWide(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","products":[]},{"id":"bob","username":"bob","products":[]}]}`)
	workspace := &mockWorkspaceAPI{files: map[string]string{
		"_users/alice/Chats/Work/projects/research/product.json": `{"schema_version":1,"product":"work","id":"research","title":"Research"}`,
		"_users/bob/Chats/Work/projects/private/product.json":    `{"schema_version":1,"product":"work","id":"private","title":"Private"}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "alice", Username: "alice"})
	logical, roots, err := authorizeWorkflowContextPathsWithReadRoots(ctx, []string{"Chats/Work/projects/research", "Chats/Work/projects/research/"})
	if err != nil || !reflect.DeepEqual(logical, []string{"Chats/Work/projects/research"}) || !reflect.DeepEqual(roots, []string{"_users/alice/Chats/Work/projects/research"}) {
		t.Fatalf("Crew context logical=%v roots=%v err=%v", logical, roots, err)
	}
	writes, reads := collectSplitFolderGuardFolders("", roots)
	if len(writes) != 0 || !reflect.DeepEqual(reads, roots) {
		t.Fatalf("reference roots arrive as the read set (Crew roots are split into writes at guard setup): writes=%v reads=%v", writes, reads)
	}
	if got, err := authorizeWorkflowContextPaths(ctx, []string{"Chats/Work/projects/research"}); err == nil || got != nil {
		t.Fatalf("workflow-only authorizer accepted Crew path: %v %v", got, err)
	}
	if got, readRoots, err := authorizeWorkflowContextPathsWithReadRoots(ctx, []string{"_users/bob/Chats/Work/projects/private"}); err != nil || !reflect.DeepEqual(readRoots, []string{"_users/bob/Chats/Work/projects/private"}) {
		t.Fatalf("another owner's Crew must be reachable: %v %v %v", got, readRoots, err)
	}
	crewWrite, readOnly := splitCrewReferenceFolders([]string{"_users/bob/Chats/Work/projects/private", "Workflow/reports"})
	if !reflect.DeepEqual(crewWrite, []string{"_users/bob/Chats/Work/projects/private/"}) || !reflect.DeepEqual(readOnly, []string{"Workflow/reports"}) {
		t.Fatalf("crew roots must be writable and workflows read-only: write=%v read=%v", crewWrite, readOnly)
	}
	for _, path := range []string{"Chats/Work/projects/private", "_users/bob/Chats/Work/projects/missing", "Chats/Work/projects/research/code"} {
		if got, readRoots, err := authorizeWorkflowContextPathsWithReadRoots(ctx, []string{path}); err == nil || got != nil || readRoots != nil {
			t.Fatalf("unauthorized Crew reference accepted: %q => %v %v %v", path, got, readRoots, err)
		}
	}
}

func TestWorkflowContextAccessFailsClosedWhenWorkspaceUnavailable(t *testing.T) {
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	if got, err := authorizeWorkflowContextPaths(context.Background(), []string{"Workflow/target"}); err == nil || got != nil {
		t.Fatalf("workspace failure granted access: %v %v", got, err)
	}
}

func TestMergeDurableWorkflowContextPaths(t *testing.T) {
	source := WorkflowManifest{ID: "source", Label: "Source", WorkflowContextPaths: []string{"Workflow/target"}}
	raw, _ := json.Marshal(source)
	workspace := &mockWorkspaceAPI{files: map[string]string{manifestPath("Workflow/source"): string(raw)}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)

	got := mergeDurableWorkflowContextPaths(context.Background(), "Workflow/source", []string{"Workflow/temporary", "Workflow/target"})
	want := []string{"Workflow/target", "Workflow/temporary"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged paths = %v, want %v", got, want)
	}
	if got := mergeDurableWorkflowContextPaths(context.Background(), "Chats/project", []string{"Workflow/temporary"}); !reflect.DeepEqual(got, []string{"Workflow/temporary"}) {
		t.Fatalf("product path unexpectedly loaded a workflow manifest: %v", got)
	}
}

// A person's own Code project can be attached, read-only, to another of their Code projects, and nowhere else: not to a Crew turn,
// not by another owner's spelling, and not when product.json says it is not a Code project.
func TestCodeProjectContextIsOwnReadOnlyAndCodeTurnsOnly(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","products":[]},{"id":"bob","username":"bob","products":[]}]}`)
	workspace := &mockWorkspaceAPI{files: map[string]string{
		"_users/alice/Chats/Code/projects/api/product.json":     `{"schema_version":1,"product":"code","id":"api","title":"API"}`,
		"_users/alice/Chats/Code/projects/notcode/product.json": `{"schema_version":1,"product":"work","id":"x","title":"X"}`,
		"_users/bob/Chats/Code/projects/private/product.json":   `{"schema_version":1,"product":"code","id":"p","title":"Private"}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	alice := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "alice", Username: "alice"})
	codeTurn := context.WithValue(alice, codeContextAllowedKey{}, true)

	if got, _, err := authorizeWorkflowContextPathsWithReadRoots(alice, []string{"Chats/Code/projects/api"}); err == nil || got != nil {
		t.Fatalf("a Code project must not be attached outside a Code turn: %v %v", got, err)
	}
	logical, roots, err := authorizeWorkflowContextPathsWithReadRoots(codeTurn, []string{"Chats/Code/projects/api", "Chats/Code/projects/api/"})
	if err != nil || !reflect.DeepEqual(logical, []string{"Chats/Code/projects/api"}) || !reflect.DeepEqual(roots, []string{"_users/alice/Chats/Code/projects/api"}) {
		t.Fatalf("own Code project: logical=%v roots=%v err=%v", logical, roots, err)
	}
	if writes, reads := splitCrewReferenceFolders(roots); len(writes) != 0 || !reflect.DeepEqual(reads, roots) {
		t.Fatalf("an attached Code project is read-only: writes=%v reads=%v", writes, reads)
	}
	for _, path := range []string{"_users/bob/Chats/Code/projects/private", "Chats/Code/projects/private", "Chats/Code/projects/notcode", "Chats/Code/projects/api/code", "Chats/Code/projects/../projects/api"} {
		if got, readRoots, err := authorizeWorkflowContextPathsWithReadRoots(codeTurn, []string{path}); err == nil || got != nil || readRoots != nil {
			t.Fatalf("unauthorized Code reference accepted: %q => %v %v %v", path, got, readRoots, err)
		}
	}
}
