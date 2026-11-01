package server

import (
	"context"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
)

func stubCrewLookups(t *testing.T, owners map[string]string, aliases map[string]string) {
	t.Helper()
	prevOwners := crewOwners.read
	crewOwners.read = func(_ context.Context, root string) string { return owners[root] }
	crewOwners.mu.Lock()
	crewOwners.entries = map[string]crewOwnerEntry{}
	crewOwners.mu.Unlock()
	t.Cleanup(func() {
		crewOwners.read = prevOwners
		crewOwners.mu.Lock()
		crewOwners.entries = map[string]crewOwnerEntry{}
		crewOwners.mu.Unlock()
	})
}

// Only the shared root Crew/<id> names a Crew; the retired Chats/Work/projects spellings are not Crew paths.
func TestParseCrewPathSpellings(t *testing.T) {
	got, ok := parseCrewPath("alice", "/Crew/sde-1a2b/code/reports/x.py/")
	if want := (crewPathRef{Root: "Crew/sde-1a2b", Rest: "code/reports/x.py", Shared: true}); !ok || got != want {
		t.Fatalf("got %+v %v, want %+v", got, ok, want)
	}
	for _, raw := range []string{"", "Crew", "Crew/", "Crew/.hidden", "Workflow/x", "Crew/../Workflow/x",
		"Chats/Work/projects/sde-1a2b", "_users/bob/Chats/Work/projects/ops-9f", "Chats/ExampleApp/activities/a"} {
		if ref, ok := parseCrewPath("alice", raw); ok {
			t.Fatalf("%q parsed as %+v", raw, ref)
		}
	}
}

// A Crew takes its owner from the server's registry; one without an owner is nobody's.
func TestResolveCrewPathOwner(t *testing.T) {
	stubCrewLookups(t, map[string]string{"Crew/sde-1a2b": "alice"}, nil)
	ref, ok := resolveCrewPath(context.Background(), "bob", "Crew/sde-1a2b/db")
	if !ok || ref.Root != "Crew/sde-1a2b" || ref.Rest != "db" || ref.OwnerID != "alice" || !ref.Shared {
		t.Fatalf("resolved to %+v", ref)
	}
	ref, _ = resolveCrewPath(context.Background(), "alice", "Crew/orphan")
	if ref.OwnerID != "" || crewAccessFor(&UserClaims{UserID: "alice"}, ref) != crewAccessNone {
		t.Fatalf("orphan crew has an owner: %+v", ref)
	}
}

func TestCrewAccessFor(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","products":["work"]},{"id":"bob","username":"bob","products":["work"]},{"id":"carol","username":"carol","products":["agentworks"]}]}`)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	// A Crew starts private; another user reads it only once the owner has shared it.
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "sde", OwnerID: "alice", Shared: true}); err != nil {
		t.Fatal(err)
	}
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
