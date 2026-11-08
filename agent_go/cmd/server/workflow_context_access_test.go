package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
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
	// A turn does not fail for an attachment its sender cannot read (the owner
	// shared the product; Run mode and functions run on the owner's context): the
	// attachment is left out, so its files are never read. The strict check used
	// to attach or grant still refuses.
	req := &QueryRequest{WorkflowContextPaths: []string{"Workflow/target"}}
	if err := admitTurnContextPaths(ctxFor("other"), req); err != nil || len(req.WorkflowContextPaths) != 0 || len(req.authorizedWorkflowContextReadPaths) != 0 {
		t.Fatalf("unreadable attachment: err=%v paths=%v reads=%v", err, req.WorkflowContextPaths, req.authorizedWorkflowContextReadPaths)
	}
	if note := omittedWorkflowContextNote(req.omittedWorkflowContextCount); req.omittedWorkflowContextCount != 1 || !strings.Contains(note, "1 attached") || strings.Contains(note, "target") {
		t.Fatalf("the agent must be told, by count only, that context was left out: count=%d note=%q", req.omittedWorkflowContextCount, note)
	}
}

// PLAT-746-502: a registered Crew is usable only by its owner or current audience.
func TestCrewContextAccessRequiresCurrentAudience(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","products":[]},{"id":"bob","username":"bob","products":[]}]}`)
	own := registerTestCrew(t, "research", "alice", false)
	shared := registerTestCrew(t, "shared", "bob", true)
	private := registerTestCrew(t, "private", "bob", false)
	workspace := &mockWorkspaceAPI{files: map[string]string{
		own + "/product.json":     `{"schema_version":1,"product":"work","id":"crew-research","title":"Research"}`,
		shared + "/product.json":  `{"schema_version":1,"product":"work","id":"crew-shared","title":"Shared"}`,
		private + "/product.json": `{"schema_version":1,"product":"work","id":"crew-private","title":"Private"}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	ctxFor := func(user string) context.Context {
		return context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: user})
	}
	for _, allowed := range []struct{ user, root string }{{"alice", own}, {"alice", shared}, {"bob", private}} {
		logical, roots, err := authorizeWorkflowContextPathsWithReadRoots(ctxFor(allowed.user), []string{allowed.root, allowed.root + "/"})
		if err != nil || !reflect.DeepEqual(logical, []string{allowed.root}) || !reflect.DeepEqual(roots, []string{allowed.root}) {
			t.Fatalf("admitted Crew %s/%s: logical=%v roots=%v err=%v", allowed.user, allowed.root, logical, roots, err)
		}
		writes, reads := splitCrewReferenceFolders(append(roots, "Workflow/reports"))
		if !reflect.DeepEqual(writes, []string{allowed.root + "/"}) || !reflect.DeepEqual(reads, []string{"Workflow/reports"}) {
			t.Fatalf("admitted Crew files writable, workflow read-only: writes=%v reads=%v", writes, reads)
		}
	}
	if got, err := authorizeWorkflowContextPaths(ctxFor("alice"), []string{own}); err == nil || got != nil {
		t.Fatalf("workflow-only authorizer accepted Crew path: %v %v", got, err)
	}
	for _, denied := range []struct{ user, root string }{{"alice", private}, {"", own}, {"alice", "Crew/missing"}, {"alice", own + "/code"}, {"alice", "Chats/Work/projects/research"}} {
		if got, roots, err := authorizeWorkflowContextPathsWithReadRoots(ctxFor(denied.user), []string{denied.root}); err == nil || got != nil || roots != nil {
			t.Fatalf("unauthorized reference accepted: %s/%s => %v %v %v", denied.user, denied.root, got, roots, err)
		}
	}
	if err := defaultProjectOwners().SetAudience("work", "shared", true, nil); err != nil {
		t.Fatal(err)
	}
	if got, roots, err := authorizeWorkflowContextPathsWithReadRoots(ctxFor("alice"), []string{shared}); err == nil || got != nil || roots != nil {
		t.Fatalf("revoked Crew reference remained usable: %v %v %v", got, roots, err)
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

// Code routing retired in8106ccb9de; old private paths never become Crew context.
func TestContextReferencesRejectRetiredCodePaths(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","products":[]}]}`)
	workspace := &mockWorkspaceAPI{files: map[string]string{
		"_users/alice/Chats/Code/projects/api/product.json": `{"schema_version":1,"product":"code","id":"api","title":"API"}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "alice"})
	for _, path := range []string{"Chats/Code/projects/api", "_users/alice/Chats/Code/projects/api", "_users/bob/Chats/Code/projects/private", "Chats/Code/projects/api/code", "Chats/Code/projects/../projects/api"} {
		if got, roots, err := authorizeWorkflowContextPathsWithReadRoots(ctx, []string{path}); err == nil || got != nil || roots != nil {
			t.Fatalf("retired private path accepted: %q => %v %v %v", path, got, roots, err)
		}
	}
}
