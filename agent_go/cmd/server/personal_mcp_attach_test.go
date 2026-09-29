package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func TestPlaceRootOfAndCleanAttachRoot(t *testing.T) {
	cases := map[string]string{
		"Workflow/w":                            "Workflow/w",
		"Workflow/w/runs/iteration-3/logs":      "Workflow/w",
		"/Workflow/w/":                          "Workflow/w",
		"Workflow/.relay_releases/x":            "",
		"Crew/abc/files/x":                      "Crew/abc",
		"_users/alice/Chats/Work/projects/p/db": "_users/alice/Chats/Work/projects/p",
		"_users/alice/Chats/Code/projects/p":    "",
		"Chats/Work/projects/p":                 "",
		"Workflow/../Workflow/w":                "",
		"Downloads/x":                           "",
	}
	for in, want := range cases {
		if got := placeRootOf(in); got != want {
			t.Errorf("placeRootOf(%q) = %q, want %q", in, got, want)
		}
	}
	if got := attachRootForCaller("alice", "Chats/Work/projects/p"); got != "_users/alice/Chats/Work/projects/p" {
		t.Errorf("own Crew logical path = %q", got)
	}
}

// A workflow's own connection: added by someone who can edit it, used by
// every run there, invisible in that person's Code, and gone with their
// access or when removed.
func TestPlaceMCPConnectionLifecycle(t *testing.T) {
	_, api := readAccessRouterWithAPI(t)
	withPersonalMCPRoot(t)
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	ctx := context.Background()
	do := func(handler http.HandlerFunc, method, target, user, body string, vars map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		if vars != nil {
			req = mux.SetURLVars(req, vars)
		}
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}

	if !personalMCPCanAttach(ctx, "alice", "Workflow/w") || personalMCPCanAttach(ctx, "bob", "Workflow/w") || personalMCPCanAttach(ctx, "bob", "Workflow/shared") {
		t.Fatal("only people who can edit a workflow may add connections to it")
	}

	// bob (read-only on Workflow/shared) cannot add one.
	if rec := do(api.handleAddPlaceMCP, http.MethodPost, "/api/mcp/place", "bob",
		`{"workspace_path":"Workflow/shared","name":"gmail","url":"https://mcp.example.com/mcp","transport":"http","headers":{}}`, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("reader adding a connection: %d %s", rec.Code, rec.Body.String())
	}

	// alice adds her own server to Workflow/w (stored directly: the add route
	// probes the URL over the network).
	store := placeMCPStoreID("alice", "Workflow/w")
	if _, err := addPersonalMCPServer(store, personalMCPServer{Name: "gmail", URL: "https://mcp.example.com/mcp", Transport: "http"}); err != nil {
		t.Fatal(err)
	}
	if err := recordPlaceMCP("alice", "gmail", "Workflow/w"); err != nil {
		t.Fatal(err)
	}

	// Every run in the workflow gets it, under a name unique to this place.
	names, overrides := attachedMCPServersForRoot(ctx, "Workflow/w/runs/iteration-0")
	if len(names) != 1 || overrides[names[0]].Server == nil || overrides[names[0]].Server.URL != "https://mcp.example.com/mcp" {
		t.Fatalf("workflow connections = %v %+v", names, overrides)
	}
	if names[0] == personalMCPInternalName("alice", "gmail") {
		t.Fatal("a place connection must not share alice's Code server name")
	}
	if other, _ := attachedMCPServersForRoot(ctx, "Workflow/shared"); len(other) != 0 {
		t.Fatalf("connection leaked to another workflow: %v", other)
	}

	// It never shows among alice's own (Code) servers.
	if own, _ := listPersonalMCPServers("alice"); len(own) != 0 {
		t.Fatalf("place connection listed in alice's Code servers: %+v", own)
	}

	// Anyone who can read the workflow sees it listed, with its owner.
	rec := do(api.handleListPlaceMCP, http.MethodGet, "/api/mcp/place?workspace_path=Workflow/w", "alice", "", nil)
	var listed struct {
		Servers []struct {
			Name, OwnerName string
			Mine, Active    bool
		}
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &listed)
	if rec.Code != http.StatusOK || len(listed.Servers) != 1 || listed.Servers[0].Name != "gmail" || !listed.Servers[0].Mine || !listed.Servers[0].Active {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(api.handleListPlaceMCP, http.MethodGet, "/api/mcp/place?workspace_path=Workflow/w", "bob", "", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("stranger listing: %d", rec.Code)
	}

	// bob cannot remove alice's connection; alice can.
	if rec := do(api.handleRemovePlaceMCP, http.MethodDelete, "/api/mcp/place/gmail?workspace_path=Workflow/w&owner=alice", "bob", "", map[string]string{"name": "gmail"}); rec.Code != http.StatusForbidden {
		t.Fatalf("stranger removing: %d", rec.Code)
	}
	if rec := do(api.handleRemovePlaceMCP, http.MethodDelete, "/api/mcp/place/gmail?workspace_path=Workflow/w", "alice", "", map[string]string{"name": "gmail"}); rec.Code != http.StatusOK {
		t.Fatalf("owner removing: %d %s", rec.Code, rec.Body.String())
	}
	if names, _ := attachedMCPServersForRoot(ctx, "Workflow/w"); len(names) != 0 {
		t.Fatalf("removed connection still used: %v", names)
	}
	if left, _ := listPersonalMCPServers(store); len(left) != 0 {
		t.Fatalf("removed connection's server still stored: %+v", left)
	}
}

// A connection whose owner can no longer edit the workflow stops at once.
func TestPlaceMCPStopsWhenOwnerLosesEditAccess(t *testing.T) {
	readAccessRouterWithAPI(t)
	withPersonalMCPRoot(t)
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	// bob only reads Workflow/shared: a record naming him (planted, or left
	// from before a demotion) grants nothing.
	store := placeMCPStoreID("bob", "Workflow/shared")
	if _, err := addPersonalMCPServer(store, personalMCPServer{Name: "gmail", URL: "https://mcp.example.com/mcp", Transport: "http"}); err != nil {
		t.Fatal(err)
	}
	if err := recordPlaceMCP("bob", "gmail", "Workflow/shared"); err != nil {
		t.Fatal(err)
	}
	if names, _ := attachedMCPServersForRoot(context.Background(), "Workflow/shared"); len(names) != 0 {
		t.Fatalf("connection of someone who cannot edit is used: %v", names)
	}
}
