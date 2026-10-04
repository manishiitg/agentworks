package store

import (
	"path/filepath"
	"testing"
)

func platformSecretNames(s *MemoryStore, group string) map[string]bool {
	out := map[string]bool{}
	for _, row := range s.ListSecrets("w", "", group, false) {
		out[row.Name] = true
	}
	return out
}

// Existing shared secrets and servers are registered before the first Vault
// install: the first EnsurePlatformGroup must grandfather every one of them.
func TestPlatformGroupGrandfathersExistingSecretsAndServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.AddWorkspace(Workspace{ID: "w"})
	s.AddUser(User{ID: "alice", WorkspaceID: "w"})
	s.AddGroup(Group{ID: "other", WorkspaceID: "w", Name: "Other"})
	if err = s.RegisterSecrets("w", []SecretResource{{Name: "MANAGED_KEY", Managed: true}, {Name: "ENV_KEY"}}); err != nil {
		t.Fatal(err)
	}
	s.AddConnector(Connector{ID: "c1", WorkspaceID: "w", Status: StatusActive})
	s.AddConnector(Connector{ID: "c2", WorkspaceID: "w", Status: StatusActive})
	platform := PlatformGroupID("w")
	if err = s.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	if got := platformSecretNames(s, platform); !got["MANAGED_KEY"] || !got["ENV_KEY"] || len(got) != 2 {
		t.Fatalf("secrets not grandfathered: %v", got)
	}
	if !s.GroupHasServer(platform, "c1") || !s.GroupHasServer(platform, "c2") || !s.HasServerGrant("alice", "c1") {
		t.Fatal("servers not grandfathered")
	}
	if len(platformSecretNames(s, "other")) != 0 || s.GroupHasServer("other", "c1") {
		t.Fatal("other groups were granted")
	}
	// Idempotent: re-running changes nothing and appends no further events.
	before := len(s.ListPolicyEvents("w"))
	for i := 0; i < 2; i++ {
		if err = s.EnsurePlatformGroup("w"); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.ListPolicyEvents("w")) != before || len(platformSecretNames(s, platform)) != 2 {
		t.Fatal("re-run was not idempotent")
	}
	// New items registered later are granted automatically.
	if err = s.RegisterSecrets("w", []SecretResource{{Name: "LATER_KEY"}}); err != nil {
		t.Fatal(err)
	}
	s.AddConnector(Connector{ID: "c3", WorkspaceID: "w", Status: StatusActive})
	if !platformSecretNames(s, platform)["LATER_KEY"] || !s.GroupHasServer(platform, "c3") {
		t.Fatal("new items were not auto-granted")
	}
	if ok := s.AddConnectorUnique(Connector{ID: "c4", WorkspaceID: "w", Provider: "p4", Status: StatusActive}); !ok || !s.GroupHasServer(platform, "c4") {
		t.Fatal("unique connector was not auto-granted")
	}
	// Re-adding an existing connector (an update) never restores a removed grant.
	s.RevokeGroupServerGrant(platform, "c3")
	s.AddConnector(Connector{ID: "c3", WorkspaceID: "w", Status: StatusActive, Label: "renamed"})
	if s.GroupHasServer(platform, "c3") {
		t.Fatal("connector update restored a revoked grant")
	}
	s.Close()
}

func TestPlatformRevokesPersistAcrossEnsureRegisterAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.AddWorkspace(Workspace{ID: "w"})
	s.AddUser(User{ID: "alice", WorkspaceID: "w"})
	s.AddGroup(Group{ID: "other", WorkspaceID: "w", Name: "Other"})
	s.AddConnector(Connector{ID: "keep", WorkspaceID: "w", Status: StatusActive})
	s.AddConnector(Connector{ID: "drop", WorkspaceID: "w", Status: StatusActive})
	s.AddConnector(Connector{ID: "drop2", WorkspaceID: "w", Status: StatusActive})
	if err = s.RegisterSecrets("w", []SecretResource{{Name: "KEEP"}, {Name: "DROP"}}); err != nil {
		t.Fatal(err)
	}
	platform := PlatformGroupID("w")
	if err = s.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	// Another group's grants must be untouched by Platform revokes.
	if err = s.SetSecretGrant("w", "other", "DROP", "admin", true); err != nil {
		t.Fatal(err)
	}
	s.AddGroupServerGrant("other", "drop")
	if err = s.SetSecretGrant("w", platform, "DROP", "admin", false); err != nil {
		t.Fatal(err)
	}
	s.RevokeGroupServerGrant(platform, "drop")
	if !s.RemoveGroupConnectorAccess("w", platform, "drop2", "admin") {
		t.Fatal("remove access failed")
	}
	check := func(label string) {
		t.Helper()
		got := platformSecretNames(s, platform)
		if got["DROP"] || !got["KEEP"] || s.GroupHasServer(platform, "drop") || s.GroupHasServer(platform, "drop2") || !s.GroupHasServer(platform, "keep") {
			t.Fatalf("%s: revoke not honoured: %v", label, got)
		}
		if !platformSecretNames(s, "other")["DROP"] || !s.GroupHasServer("other", "drop") {
			t.Fatalf("%s: other group's grants changed", label)
		}
	}
	check("after revoke")
	if err = s.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	if err = s.RegisterSecrets("w", []SecretResource{{Name: "KEEP"}, {Name: "DROP"}}); err != nil {
		t.Fatal(err)
	}
	check("after ensure + sync")
	s.Close()
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	check("after restart")
	// An admin re-grant clears the record; a later start keeps it granted.
	if err = s.SetSecretGrant("w", platform, "DROP", "admin", true); err != nil {
		t.Fatal(err)
	}
	s.AddGroupServerGrant(platform, "drop")
	s.RevokeGroupServerGrant("other", "drop") // non-Platform revoke is not recorded
	if err = s.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	if !platformSecretNames(s, platform)["DROP"] || !s.GroupHasServer(platform, "drop") || s.GroupHasServer(platform, "drop2") {
		t.Fatal("re-grant did not stick or cleared the wrong entry")
	}
	if len(s.platformRevoked) != 1 || !s.platformRevoked["server:drop2"] {
		t.Fatal(s.platformRevoked)
	}
	// Deleting the secret and registering the name again is a new item.
	s.SetSecretGrant("w", platform, "KEEP", "admin", false)
	if err = s.DeleteSecret("w", "KEEP", "admin"); err != nil {
		t.Fatal(err)
	}
	if err = s.RegisterSecrets("w", []SecretResource{{Name: "KEEP"}}); err != nil {
		t.Fatal(err)
	}
	if !platformSecretNames(s, platform)["KEEP"] {
		t.Fatal("re-created secret was not auto-granted")
	}
}

func TestUnboundUserFirstRequestJoinsPlatformAndInheritsGrants(t *testing.T) {
	s := NewMemoryStore()
	s.AddWorkspace(Workspace{ID: "w"})
	if err := s.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterSecrets("w", []SecretResource{{Name: "KEY"}}); err != nil {
		t.Fatal(err)
	}
	s.AddConnector(Connector{ID: "c", WorkspaceID: "w", Status: StatusActive})
	if _, known := s.GetUser("newcomer"); known || len(s.ListSecrets("w", "newcomer", "", false)) != 0 {
		t.Fatal("unbound user had access")
	}
	if err := s.EnsurePlatformUser("w", "newcomer"); err != nil {
		t.Fatal(err)
	}
	if len(s.ListSecrets("w", "newcomer", "", false)) != 1 || !s.HasServerGrant("newcomer", "c") {
		t.Fatal("first request did not give Platform access")
	}
}
