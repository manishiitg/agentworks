package policy

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/access"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

func TestAuthorizeRequiresCurrentWorkspacePrincipal(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(store.Workspace{ID: "w"})
	s.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Status: store.StatusActive})
	s.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: "p__t", Fingerprint: "v1"})
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
	// A changed definition keeps the package: same member allowed, same conditions enforced, same outsider refused.
	changed := s.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: "p__t", Fingerprint: "v2"})
	if _, err := Authorize(s, alice, "p__t"); err != nil {
		t.Fatalf("package lost on a definition change: %v", err)
	}
	if err := AuthorizeArguments(s, alice, changed, map[string]any{"project": "two"}); !errors.Is(err, ErrArgumentsDenied) {
		t.Fatalf("condition dropped on a definition change: %v", err)
	}
	if _, err := Authorize(s, bob, "p__t"); !errors.Is(err, ErrNoGrant) {
		t.Fatalf("outsider reached the changed tool: %v", err)
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
		s.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: name, Fingerprint: "v1"})
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

func TestEveryoneGrantDoesNotExpandAnUnrelatedGroupKey(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(store.Workspace{ID: "w"})
	if err := s.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	s.AddUser(store.User{ID: "user", WorkspaceID: "w"})
	s.AddGroup(store.Group{ID: "private", WorkspaceID: "w", Name: "Private"})
	s.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Status: store.StatusActive})
	s.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: "c__read", Fingerprint: "f"})
	id := auth.Identity{UserID: "user", WorkspaceID: "w"}
	if _, err := Authorize(s, id, "c__read"); err != nil {
		t.Fatal("new server was not granted to Platform automatically", err)
	}
	s.RevokeGroupServerGrant(store.PlatformGroupID("w"), "c")
	if _, err := Authorize(s, id, "c__read"); !errors.Is(err, ErrNoGrant) {
		t.Fatal("revoked Platform server still grants tool", err)
	}
	s.AddGroupGrant(store.GroupGrant{GroupID: store.PlatformGroupID("w"), PublicName: "c__read"})
	if _, err := Authorize(s, id, "c__read"); err != nil {
		t.Fatal(err)
	}
	if _, err := Authorize(s, auth.Identity{WorkspaceID: "w", ViaGroup: "private"}, "c__read"); !errors.Is(err, ErrNoGrant) {
		t.Fatal("default group expanded unrelated group key", err)
	}
	s.RevokeGroupGrant(store.PlatformGroupID("w"), "c__read")
	if _, err := Authorize(s, id, "c__read"); !errors.Is(err, ErrNoGrant) {
		t.Fatal("revocation ignored", err)
	}
}

func TestAuthorizeSetupExcludesOtherUsersPrivateVaults(t *testing.T) {
	s := store.NewMemoryStore()
	s.AddWorkspace(store.Workspace{ID: "w"})
	s.AddUser(store.User{ID: "admin", WorkspaceID: "w"})
	s.AddUser(store.User{ID: "bob", WorkspaceID: "w"})
	adminVault, err := s.EnsurePersonalVault("w", "admin", "v-admin")
	if err != nil {
		t.Fatal(err)
	}
	bobVault, err := s.EnsurePersonalVault("w", "bob", "v-bob")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureCrewVault("w", "v-crew-bob", store.CrewVaultRootPrefix+"p1", "Bob's Crew", []string{"bob"}); err != nil {
		t.Fatal(err)
	}
	s.AddConnector(store.Connector{ID: "company", WorkspaceID: "w", Status: store.StatusActive})
	s.AddConnector(store.Connector{ID: "admin-own", WorkspaceID: "w", VaultID: adminVault, Status: store.StatusActive})
	s.AddConnector(store.Connector{ID: "bob-slack", WorkspaceID: "w", VaultID: bobVault, Status: store.StatusActive})
	s.AddConnector(store.Connector{ID: "crew-app", WorkspaceID: "w", VaultID: "v-crew-bob", Status: store.StatusActive})
	for _, tc := range [][2]string{
		{"company", "company__read"},
		{"admin-own", "admin__read"},
		{"bob-slack", "slack__read_dm"},
		{"crew-app", "crew__read"},
	} {
		s.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: tc[0], PublicName: tc[1], Fingerprint: "f"})
	}

	adminID := auth.Identity{UserID: "admin", WorkspaceID: "w"}
	if _, err := AuthorizeSetup(s, adminID, "company__read"); err != nil {
		t.Fatalf("company connector denied in setup: %v", err)
	}
	if _, err := AuthorizeSetup(s, adminID, "admin__read"); err != nil {
		t.Fatalf("admin's own personal vault denied in setup: %v", err)
	}
	if _, err := AuthorizeSetup(s, adminID, "slack__read_dm"); !errors.Is(err, ErrNoGrant) {
		t.Fatalf("another user's personal vault allowed in setup: %v", err)
	}
	if _, err := AuthorizeSetup(s, adminID, "crew__read"); !errors.Is(err, ErrNoGrant) {
		t.Fatalf("unowned crew vault allowed in setup: %v", err)
	}
}

func TestEvaluatorChecksSQLitePersistenceOnceAndServesBatchChecks(t *testing.T) {
	s, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.AddWorkspace(store.Workspace{ID: "w"})
	s.AddUser(store.User{ID: "alice", WorkspaceID: "w"})
	s.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Status: store.StatusActive})
	s.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: "p__read", Fingerprint: "f"})
	s.AddGrant(store.Grant{UserID: "alice", PublicName: "p__read"})

	eval, err := NewEvaluator(s)
	if err != nil {
		t.Fatalf("NewEvaluator failed on healthy SQLite store: %v", err)
	}
	id := auth.Identity{UserID: "alice", WorkspaceID: "w"}
	if !eval.Visible(id, "p__read") {
		t.Fatal("Evaluator.Visible denied granted tool")
	}
	if _, err := eval.AuthorizeSetup(id, "p__read"); err != nil {
		t.Fatalf("Evaluator.AuthorizeSetup denied company tool: %v", err)
	}
}
