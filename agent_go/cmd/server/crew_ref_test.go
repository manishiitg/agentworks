package server

import (
	"context"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
)

func stubCrewLookups(t *testing.T, owners map[string]string, aliases map[string]string) {
	t.Helper()
	prevOwners, prevAliases := crewOwners.read, crewPathAliases.read
	crewOwners.read = func(_ context.Context, root string) string { return owners[root] }
	crewPathAliases.read = func(context.Context) map[string]string { return aliases }
	crewOwners.mu.Lock()
	crewOwners.entries = map[string]crewOwnerEntry{}
	crewOwners.mu.Unlock()
	crewPathAliases.mu.Lock()
	crewPathAliases.aliases = nil
	crewPathAliases.mu.Unlock()
	t.Cleanup(func() {
		crewOwners.read, crewPathAliases.read = prevOwners, prevAliases
		crewOwners.mu.Lock()
		crewOwners.entries = map[string]crewOwnerEntry{}
		crewOwners.mu.Unlock()
		crewPathAliases.mu.Lock()
		crewPathAliases.aliases = nil
		crewPathAliases.mu.Unlock()
	})
}

// All three spellings of one crew parse to the same root; nothing else is a crew.
func TestParseCrewPathSpellings(t *testing.T) {
	for raw, want := range map[string]crewPathRef{
		"Chats/Work/projects/sde-1a2b":                     {Root: "_users/alice/Chats/Work/projects/sde-1a2b", OwnerID: "alice"},
		"/Chats/Work/projects/sde-1a2b/db/reports/x.html/": {Root: "_users/alice/Chats/Work/projects/sde-1a2b", Rest: "db/reports/x.html", OwnerID: "alice"},
		"_users/bob/Chats/Work/projects/ops-9f":            {Root: "_users/bob/Chats/Work/projects/ops-9f", OwnerID: "bob"},
		"Crew/sde-1a2b/code/reports/x.py":                  {Root: "Crew/sde-1a2b", Rest: "code/reports/x.py", Shared: true},
	} {
		got, ok := parseCrewPath("alice", raw)
		if !ok || got != want {
			t.Fatalf("%q: got %+v %v, want %+v", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"", "Crew", "Crew/", "Crew/.hidden", "Workflow/x", "Chats/Work/projects", "Chats/Work/projects/.x",
		"_users/bob/Chats/Work", "_users//Chats/Work/projects/p", "Chats/SparkQuill/activities/a", "Crew/../Workflow/x", "Chats/Work/projects/../../x"} {
		if ref, ok := parseCrewPath("alice", raw); ok {
			t.Fatalf("%q parsed as %+v", raw, ref)
		}
	}
	if _, ok := parseCrewPath("", "Chats/Work/projects/p"); ok {
		t.Fatal("user-relative crew path without a caller resolved")
	}
}

// After migration a legacy spelling follows the alias to Crew/<id>; a shared
// root takes its owner from the manifest.
func TestResolveCrewPathAliasAndOwner(t *testing.T) {
	stubCrewLookups(t, map[string]string{"Crew/sde-1a2b": "alice"}, map[string]string{"_users/alice/Chats/Work/projects/sde-1a2b": "Crew/sde-1a2b"})
	for _, raw := range []string{"Chats/Work/projects/sde-1a2b/db", "_users/alice/Chats/Work/projects/sde-1a2b/db", "Crew/sde-1a2b/db"} {
		ref, ok := resolveCrewPath(context.Background(), "alice", raw)
		if !ok || ref.Root != "Crew/sde-1a2b" || ref.Rest != "db" || ref.OwnerID != "alice" || !ref.Shared {
			t.Fatalf("%q resolved to %+v", raw, ref)
		}
	}
	// Not migrated: stays where it is.
	ref, _ := resolveCrewPath(context.Background(), "bob", "Chats/Work/projects/ops")
	if ref.Root != "_users/bob/Chats/Work/projects/ops" || ref.Shared {
		t.Fatalf("unmigrated crew moved: %+v", ref)
	}
	// A shared crew without a readable owner is nobody's.
	ref, _ = resolveCrewPath(context.Background(), "alice", "Crew/orphan")
	if ref.OwnerID != "" || crewAccessFor(&UserClaims{UserID: "alice"}, ref) != crewAccessNone {
		t.Fatalf("orphan crew has an owner: %+v", ref)
	}
}

func TestCrewAccessFor(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","products":["work"]},{"id":"bob","username":"bob","products":["work"]},{"id":"carol","username":"carol","products":["agentworks"]}]}`)
	ref := crewPathRef{Root: "Crew/sde", OwnerID: "alice", Shared: true}
	for user, want := range map[string]crewAccessLevel{"alice": crewAccessOwner, "bob": crewAccessReader, "carol": crewAccessNone} {
		if got := crewAccessFor(&UserClaims{UserID: user, Username: user}, ref); got != want {
			t.Fatalf("%s: %v, want %v", user, got, want)
		}
	}
	if crewAccessFor(nil, ref) != crewAccessNone {
		t.Fatal("anonymous caller has crew access")
	}
}

// Crew/ through the raw workspace proxy: the owner only; never the bare
// root, a crew someone else owns, or one without an owner (not created yet).
func TestWorkspaceProxyGatesSharedCrewRoot(t *testing.T) {
	stubCrewLookups(t, map[string]string{"Crew/sde": "alice"}, nil)
	for raw, blocked := range map[string]bool{
		"Crew/sde/db/reports/index.html": false,
		"Crew/sde":                       false,
		"Crew":                           true,
		"Crew/new-crew/product.json":     true,
		"Workflow/x/plan.json":           false,
	} {
		if got := workspaceProxyPathIsOtherUser(raw, "alice"); got != blocked {
			t.Fatalf("alice %q: blocked=%v, want %v", raw, got, blocked)
		}
	}
	if !workspaceProxyPathIsOtherUser("Crew/sde/db/db.sqlite", "bob") {
		t.Fatal("a reader got raw access to someone else's crew")
	}
	if !workspaceProxyURLIsOtherUser("api/documents/Crew/sde/product.json", "bob") {
		t.Fatal("URL form not gated")
	}
}

// A shared crew root is never a free-form selected folder.
func TestCleanAgentProfileWorkspaceRejectsSharedCrewRoot(t *testing.T) {
	for _, raw := range []string{"Crew", "Crew/sde", "Crew/sde/code"} {
		if _, err := cleanAgentProfileWorkspace(raw, "alice"); err == nil {
			t.Fatalf("%q accepted as a selected folder", raw)
		}
	}
	if got, err := cleanAgentProfileWorkspace("Chats/Work/projects/sde", "alice"); err != nil || got != "Chats/Work/projects/sde" {
		t.Fatalf("legacy crew folder changed: %q %v", got, err)
	}
}

// Live feed: a shared crew is a subscribable root, visible by crew access.
func TestLiveFeedSharedCrewRoot(t *testing.T) {
	if got := livefeed.WorkflowRoot("Crew/sde/db/db.sqlite"); got != "Crew/sde" {
		t.Fatalf("crew root: %q", got)
	}
	if got := livefeed.WorkflowRoot("_users/alice/Chats/Work/projects/sde/db"); got != "" {
		t.Fatalf("legacy crew path became a feed root: %q", got)
	}
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","products":["work"]},{"id":"carol","username":"carol","products":["agentworks"]}]}`)
	stubCrewLookups(t, map[string]string{"Crew/sde": "alice"}, nil)
	if !newLiveFeedAccess(&UserClaims{UserID: "alice", Username: "alice"}).visible(context.Background(), "Crew/sde") {
		t.Fatal("owner cannot see their crew's notices")
	}
	if newLiveFeedAccess(&UserClaims{UserID: "carol", Username: "carol"}).visible(context.Background(), "Crew/sde") {
		t.Fatal("user without the Crew product sees crew notices")
	}
}
