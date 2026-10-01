package policy

import (
	"errors"
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/access"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

func TestAuthorizeRequiresCurrentWorkspacePrincipal(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(store.Workspace{ID: "w"})
	s.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Status: store.StatusActive})
	tool := s.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: "p__t", Fingerprint: "v1"})
	if _, ok := s.ApproveTool("w", tool.PublicName, tool.Fingerprint, tool.Version); !ok {
		t.Fatal("approve tool")
	}
	s.AddGrant(store.Grant{UserID: "u", PublicName: "p__t"})
	if _, err := Authorize(s, auth.Identity{UserID: "u", WorkspaceID: "w"}, "p__t"); !errors.Is(err, ErrUnknownUser) {
		t.Fatalf("orphaned grant allowed: %v", err)
	}
	s.AddUser(store.User{ID: "u", WorkspaceID: "w"})
	if _, err := Authorize(s, auth.Identity{UserID: "u", WorkspaceID: "w"}, "p__t"); err != nil {
		t.Fatalf("current user denied: %v", err)
	}
	s.AddGroupServerGrant("deleted", "c")
	if _, err := Authorize(s, auth.Identity{WorkspaceID: "w", ViaGroup: "deleted"}, "p__t"); !errors.Is(err, ErrUnknownGroup) {
		t.Fatalf("orphaned group key allowed: %v", err)
	}
}

func TestPublishedPackageOverridesBroadGrantAndChecksArguments(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(store.Workspace{ID: "w"})
	s.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Status: store.StatusActive})
	s.AddUser(store.User{ID: "alice", WorkspaceID: "w"})
	s.AddUser(store.User{ID: "bob", WorkspaceID: "w"})
	s.AddGroup(store.Group{ID: "g", WorkspaceID: "w"})
	s.AddMember("g", "alice")
	s.AddGroupServerGrant("g", "c")
	s.AddGrant(store.Grant{UserID: "bob", PublicName: "p__t"})
	tool := s.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: "p__t", Fingerprint: "v1"})
	tool, _ = s.ApproveTool("w", tool.PublicName, tool.Fingerprint, tool.Version)
	draft, ok := s.SavePackageDraft(access.Package{ID: "pkg", WorkspaceID: "w", GroupID: "g", Name: "Scoped", Rules: []access.ToolRule{{PublicName: "p__t", Fingerprint: "v1", Conditions: []access.Condition{{Path: "/project", Op: "equals", Value: "one"}}}}}, 0)
	if !ok {
		t.Fatal("save draft")
	}
	if _, ok := s.PublishPackage("w", draft.ID, draft.Version); !ok {
		t.Fatal("publish")
	}
	alice := auth.Identity{UserID: "alice", WorkspaceID: "w"}
	bob := auth.Identity{UserID: "bob", WorkspaceID: "w"}
	if _, err := Authorize(s, alice, "p__t"); err != nil {
		t.Fatalf("member denied: %v", err)
	}
	if _, err := Authorize(s, bob, "p__t"); !errors.Is(err, ErrNoGrant) {
		t.Fatalf("broad direct grant bypassed package: %v", err)
	}
	if err := AuthorizeArguments(s, alice, tool, map[string]any{"project": "one"}); err != nil {
		t.Fatalf("matching argument denied: %v", err)
	}
	for _, args := range []map[string]any{{"project": "two"}, {}, {"project": 1}} {
		if err := AuthorizeArguments(s, alice, tool, args); !errors.Is(err, ErrArgumentsDenied) {
			t.Fatalf("argument bypass: %v", args)
		}
	}
	changed := s.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: "p__t", Fingerprint: "v2"})
	if _, ok := s.ApproveTool("w", changed.PublicName, changed.Fingerprint, changed.Version); !ok {
		t.Fatal("approve changed tool")
	}
	if _, err := Authorize(s, alice, "p__t"); !errors.Is(err, ErrNoGrant) {
		t.Fatalf("old package survived schema change: %v", err)
	}
	if _, ok := s.RevokePackage("w", "pkg"); !ok {
		t.Fatal("revoke package")
	}
	if _, err := Authorize(s, alice, "p__t"); !errors.Is(err, ErrNoGrant) {
		t.Fatalf("legacy broad grant returned after revoke: %v", err)
	}
}

func TestRemovingToolFromPackageDoesNotRestoreLegacyGrant(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(store.Workspace{ID: "w"})
	s.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Status: store.StatusActive})
	s.AddUser(store.User{ID: "u", WorkspaceID: "w"})
	s.AddGroup(store.Group{ID: "g", WorkspaceID: "w"})
	s.AddMember("g", "u")
	s.AddGroupServerGrant("g", "c")
	for _, name := range []string{"p__one", "p__two"} {
		tool := s.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: name, Fingerprint: "v1"})
		s.ApproveTool("w", name, tool.Fingerprint, tool.Version)
	}
	id := auth.Identity{UserID: "u", WorkspaceID: "w"}
	rules := []access.ToolRule{{PublicName: "p__one", Fingerprint: "v1"}, {PublicName: "p__two", Fingerprint: "v1"}}
	p, _ := s.SavePackageDraft(access.Package{ID: "p", WorkspaceID: "w", GroupID: "g", Name: "both", Rules: rules}, 0)
	s.PublishPackage("w", "p", p.Version)
	p, _ = s.SavePackageDraft(access.Package{ID: "p", WorkspaceID: "w", GroupID: "g", Name: "one", Rules: rules[:1]}, p.Version)
	s.PublishPackage("w", "p", p.Version)
	if _, err := Authorize(s, id, "p__two"); !errors.Is(err, ErrNoGrant) {
		t.Fatalf("removed tool regained broad grant: %v", err)
	}
	draft, _ := s.SavePackageDraft(access.Package{ID: "p", WorkspaceID: "w", GroupID: "g", Name: "both again", Rules: rules}, p.Version)
	s.RevokePackage("w", "p")
	if _, ok := s.PublishPackage("w", "p", draft.Version); ok {
		t.Fatal("draft from before revocation republished")
	}
}
