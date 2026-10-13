package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkflowAccessResolution(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[
	  {"id":"a1","username":"alice","admin":true,"can_create":true,"products":[]},
	  {"id":"b2","username":"bob","can_create":true,"products":[]},
	  {"id":"c3","username":"carol","can_create":false,"products":[]},
	  {"id":"d4","username":"dan","can_create":true,"products":[]},
	  {"id":"e5","username":"erin","can_create":false,"can_edit":true,"products":["agentworks"]}
	]}`)
	alice, bob, carol, dan, erin := &UserClaims{UserID: "a1"}, &UserClaims{UserID: "b2"}, &UserClaims{UserID: "c3"}, &UserClaims{UserID: "d4"}, &UserClaims{UserID: "e5"}

	owned := &WorkflowManifest{ID: "w1", CreatedBy: "b2", Access: &WorkflowAccess{Owners: []string{"b2"}, Readers: []string{"c3"}}}
	if got := workflowAccessForManifest(bob, owned); got != WorkflowAccessOwner {
		t.Fatalf("owner: %s", got)
	}
	if got := workflowAccessForManifest(carol, owned); got != WorkflowAccessRead {
		t.Fatalf("reader: %s", got)
	}
	if got := workflowAccessForManifest(dan, owned); got != WorkflowAccessNone {
		t.Fatalf("unrelated member must not see it: %s", got)
	}
	if got := workflowAccessForManifest(alice, owned); got != WorkflowAccessOwner {
		t.Fatalf("admin is owner of everything: %s", got)
	}
	contributorOwned := &WorkflowManifest{ID: "w5", Access: &WorkflowAccess{Owners: []string{"e5"}}}
	if got := workflowAccessForManifest(erin, contributorOwned); got != WorkflowAccessOwner {
		t.Fatalf("contributor may own an assigned workflow: %s", got)
	}
	if userAccessForClaims(erin).CanCreate {
		t.Fatal("contributor must not be able to create workflows")
	}

	// created_by alone names the owner on manifests written before the access block existed.
	legacyCreated := &WorkflowManifest{ID: "w2", CreatedBy: "d4"}
	if got := workflowAccessForManifest(dan, legacyCreated); got != WorkflowAccessOwner {
		t.Fatalf("created_by owner: %s", got)
	}
	if got := workflowAccessForManifest(bob, legacyCreated); got != WorkflowAccessNone {
		t.Fatalf("created_by excludes others: %s", got)
	}

	// No ownership record at all: the account tier applies, as before phase 3.
	unowned := &WorkflowManifest{ID: "w3"}
	if got := workflowAccessForManifest(bob, unowned); got != WorkflowAccessWrite {
		t.Fatalf("unowned for a member: %s", got)
	}
	if got := workflowAccessForManifest(carol, unowned); got != WorkflowAccessRead {
		t.Fatalf("unowned for a read-only account: %s", got)
	}

	// A read-only account listed as owner is still only a reader.
	listedReadOnly := &WorkflowManifest{ID: "w4", Access: &WorkflowAccess{Owners: []string{"c3"}}}
	if got := workflowAccessForManifest(carol, listedReadOnly); got != WorkflowAccessRead {
		t.Fatalf("read-only account can never edit: %s", got)
	}
}

func TestFilterAnnotatesMyAccessAndHidesUnshared(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"b2","username":"bob","can_create":true,"products":[]},{"id":"d4","username":"dan","can_create":true,"products":[]}]}`)
	discovered := []DiscoveredWorkflow{
		{WorkspacePath: "Workflow/mine", Manifest: &WorkflowManifest{ID: "mine", Access: &WorkflowAccess{Owners: []string{"b2"}}}},
		{WorkspacePath: "Workflow/shared", Manifest: &WorkflowManifest{ID: "shared", Access: &WorkflowAccess{Owners: []string{"d4"}, Readers: []string{"b2"}}}},
		{WorkspacePath: "Workflow/theirs", Manifest: &WorkflowManifest{ID: "theirs", Access: &WorkflowAccess{Owners: []string{"d4"}}}},
		{WorkspacePath: "Workflow/legacy", Manifest: &WorkflowManifest{ID: "legacy"}},
	}
	got := filterWorkflowManifestsForUser(&UserClaims{UserID: "b2"}, discovered)
	want := map[string]WorkflowAccessLevel{"Workflow/mine": WorkflowAccessOwner, "Workflow/shared": WorkflowAccessRead, "Workflow/legacy": WorkflowAccessWrite}
	if len(got) != len(want) {
		t.Fatalf("got %d workflows, want %d: %+v", len(got), len(want), got)
	}
	for _, w := range got {
		if want[w.WorkspacePath] != w.MyAccess {
			t.Fatalf("%s: my_access=%s want %s", w.WorkspacePath, w.MyAccess, want[w.WorkspacePath])
		}
	}
}

func TestContributorCanEditButCannotCreate(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"e5","username":"erin","can_create":false,"can_edit":true,"products":["agentworks"]}]}`)
	claims := &UserClaims{UserID: "e5", Username: "erin"}

	owned := &WorkflowManifest{ID: "owned", Access: &WorkflowAccess{Owners: []string{"e5"}}}
	if got := workflowAccessForManifest(claims, owned); got != WorkflowAccessOwner {
		t.Fatalf("assigned contributor access = %s, want owner", got)
	}

	createCalled := false
	handler := requireWorkflowCreateAccess(func(w http.ResponseWriter, _ *http.Request) {
		createCalled = true
		w.WriteHeader(http.StatusCreated)
	})
	rec := httptest.NewRecorder()
	handler(rec, adminRequest(http.MethodPost, "/api/workflows/manifest", `{}`, claims, nil))
	if createCalled || rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"required_access":"create"`) {
		t.Fatalf("contributor create gate = code %d body %s called=%v", rec.Code, rec.Body.String(), createCalled)
	}

	writeCalled := false
	writeHandler := requireWorkflowWriteAccess(func(w http.ResponseWriter, _ *http.Request) {
		writeCalled = true
		w.WriteHeader(http.StatusNoContent)
	})
	rec = httptest.NewRecorder()
	writeHandler(rec, adminRequest(http.MethodPut, "/api/workflows/manifest", `{}`, claims, nil))
	if !writeCalled || rec.Code != http.StatusNoContent {
		t.Fatalf("contributor write gate = code %d called=%v", rec.Code, writeCalled)
	}
}

func TestWorkflowFolderFromProxyPath(t *testing.T) {
	cases := map[string]string{
		"api/documents/Workflow/abc/plan.json": "Workflow/abc",
		"api/folders/Workflow/abc":             "Workflow/abc",
		"api/documents/Workflow":               "",
		"api/documents/Chats/x":                "",
	}
	for in, want := range cases {
		if got := workflowFolderFromWorkspaceProxyPath(in); got != want {
			t.Fatalf("%s: got %q want %q", in, got, want)
		}
	}
}

func TestSessionVisibility(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"a1","username":"alice","admin":true,"can_create":true,"products":[]},{"id":"b2","username":"bob","can_create":true,"products":[]}]}`)
	bob, alice := &UserClaims{UserID: "b2"}, &UserClaims{UserID: "a1"}
	if !sessionVisibleTo("b2", bob) || sessionVisibleTo("a1", bob) {
		t.Fatal("own session visible, someone else's not")
	}
	if sessionVisibleTo("", bob) {
		t.Fatal("an unowned session must not be visible to every member")
	}
	if !sessionVisibleTo("", alice) {
		t.Fatal("an unowned session is visible to an admin")
	}
	t.Setenv("MULTI_USER_MODE", "false")
	if !sessionVisibleTo("", &UserClaims{UserID: "default"}) {
		t.Fatal("single-user mode keeps unowned sessions visible")
	}
}

func TestSetWorkflowAccessValidation(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"b2","username":"bob","email":"bob@x.io","can_create":true,"products":[]},{"id":"c3","username":"carol","can_create":false,"products":[]}]}`)
	api := &StreamingAPI{}
	// No workflow at the path (workspace API not reachable in tests) → 404 rather than a grant.
	rec := httptest.NewRecorder()
	api.handleSetWorkflowAccess(rec, adminRequest(http.MethodPut, "/api/workflow/access", `{"workspace_path":"Workflow/nope","owners":["bob"]}`, &UserClaims{UserID: "b2"}, nil))
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusInternalServerError {
		t.Fatalf("missing workflow: %d %s", rec.Code, rec.Body.String())
	}
	// Directory picker lists enabled users only, never hashes or roles.
	rec = httptest.NewRecorder()
	api.handleUserDirectory(rec, adminRequest(http.MethodGet, "/api/users/directory", "", &UserClaims{UserID: "c3"}, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"username":"bob"`) || strings.Contains(rec.Body.String(), "password") || strings.Contains(rec.Body.String(), "can_create") {
		t.Fatalf("directory: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRunBotRouteAccessForLegacyUnownedWorkflow(t *testing.T) {
	manifest := &WorkflowManifest{ID: "legacy-workflow"}
	claims := &UserClaims{UserID: "bot:slack:channel", Provider: "bot_route", BotRouteWorkflowID: manifest.ID, BotRouteGrant: "run"}
	if got := workflowAccessForManifest(claims, manifest); got != WorkflowAccessRead {
		t.Fatalf("explicit Run route rejected legacy workflow: %s", got)
	}
	claims.BotRouteWorkflowID = "other-workflow"
	if got := workflowAccessForManifest(claims, manifest); got != WorkflowAccessNone {
		t.Fatalf("route granted access to another workflow: %s", got)
	}
	claims.BotRouteWorkflowID = manifest.ID
	claims.BotRouteGrant = "owner"
	if got := workflowAccessForManifest(claims, manifest); got != WorkflowAccessNone {
		t.Fatalf("legacy workflow granted bot authoring access: %s", got)
	}
}

func TestWorkflowEditorsGrant(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[
	  {"id":"b2","username":"bob","can_create":true,"products":[]},
	  {"id":"c3","username":"carol","can_create":false,"products":[]},
	  {"id":"e5","username":"erin","can_create":false,"can_edit":true,"products":["agentworks"]}
	]}`)
	bob, carol, erin := &UserClaims{UserID: "b2"}, &UserClaims{UserID: "c3"}, &UserClaims{UserID: "e5"}

	shared := &WorkflowManifest{ID: "w1", Access: &WorkflowAccess{
		Owners: []string{"b2"}, Editors: []string{"e5", "c3"}, Readers: []string{"c3"},
	}}
	if got := workflowAccessForManifest(erin, shared); got != WorkflowAccessWrite {
		t.Fatalf("editor: %s", got)
	}
	// A viewer account caps at read even when listed as an editor.
	if got := workflowAccessForManifest(carol, shared); got != WorkflowAccessRead {
		t.Fatalf("viewer listed as editor: %s", got)
	}
	if got := workflowAccessForManifest(bob, shared); got != WorkflowAccessOwner {
		t.Fatalf("owner: %s", got)
	}
}

// A Relay has one Builder (the creator while still listed, else the first listed owner); every other listed owner, every editor
// and every reader runs it. A workflow keeps its owner and editor tiers.
func TestRelayHasOneBuilder(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[
	  {"id":"a1","username":"alice","admin":true,"can_create":true,"products":[]},
	  {"id":"b2","username":"bob","can_create":true,"products":[]},
	  {"id":"c3","username":"carol","can_create":true,"products":[]},
	  {"id":"d4","username":"dan","can_create":true,"products":[]},
	  {"id":"e5","username":"erin","can_create":true,"products":[]}
	]}`)
	alice, bob, carol, dan, erin := &UserClaims{UserID: "a1"}, &UserClaims{UserID: "b2"}, &UserClaims{UserID: "c3"}, &UserClaims{UserID: "d4"}, &UserClaims{UserID: "e5"}
	access := &WorkflowAccess{Owners: []string{"c3", "b2"}, Editors: []string{"d4"}, Readers: []string{"e5"}}
	relay := &WorkflowManifest{ID: "r1", Kind: "relay", CreatedBy: "b2", Access: access}
	for who, want := range map[*UserClaims]WorkflowAccessLevel{bob: WorkflowAccessOwner, carol: WorkflowAccessRead, dan: WorkflowAccessRead, erin: WorkflowAccessRead, alice: WorkflowAccessOwner} {
		if got := workflowAccessForManifest(who, relay); got != want {
			t.Errorf("relay %s: got %s want %s", who.UserID, got, want)
		}
	}
	// Creator no longer listed: the first listed owner builds.
	moved := &WorkflowManifest{ID: "r2", Kind: "relay", CreatedBy: "x9", Access: &WorkflowAccess{Owners: []string{"c3", "b2"}}}
	if got := workflowAccessForManifest(carol, moved); got != WorkflowAccessOwner {
		t.Errorf("first listed owner builds: %s", got)
	}
	if got := workflowAccessForManifest(bob, moved); got != WorkflowAccessRead {
		t.Errorf("second listed owner runs: %s", got)
	}
	owners, editors, readers := relayAccessLists(relay)
	if len(owners) != 1 || owners[0] != "b2" || len(editors) != 0 || len(readers) != 3 {
		t.Errorf("shown as one owner and everyone else running: %v %v %v", owners, editors, readers)
	}
	workflow := &WorkflowManifest{ID: "w9", CreatedBy: "b2", Access: access}
	if got := workflowAccessForManifest(carol, workflow); got != WorkflowAccessOwner {
		t.Errorf("a workflow keeps several owners: %s", got)
	}
	if got := workflowAccessForManifest(dan, workflow); got != WorkflowAccessWrite {
		t.Errorf("a workflow keeps its editors: %s", got)
	}
}
