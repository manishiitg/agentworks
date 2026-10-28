package store

import "testing"

// One personal vault per person (PLAT-856-400): asking again returns the same one, each person gets their own, and
// only its owner changes what is in it.
func TestPersonalVaultRules(t *testing.T) {
	s := NewMemoryStore()
	s.AddWorkspace(Workspace{ID: "w1", Name: "w1"})
	s.AddUser(User{ID: "alice", WorkspaceID: "w1", Email: "alice@example.com"})
	s.AddUser(User{ID: "bob", WorkspaceID: "w1", Email: "bob@example.com"})
	s.AddUser(User{ID: "carol", WorkspaceID: "w1", Email: "carol@example.com"})

	mine, err := s.EnsurePersonalVault("w1", "alice", "v-1")
	if err != nil || mine != "v-1" {
		t.Fatalf("first personal vault: %q %v", mine, err)
	}
	if again, err := s.EnsurePersonalVault("w1", "alice", "v-other"); err != nil || again != "v-1" {
		t.Fatalf("a person has one personal vault, got %q %v", again, err)
	}
	if bobs, err := s.EnsurePersonalVault("w1", "bob", "v-2"); err != nil || bobs != "v-2" || s.VaultOwnedBy("bob", "v-1") {
		t.Fatalf("bob got someone else's vault: %q %v", bobs, err)
	}

	s.AddConnector(Connector{ID: "c1", WorkspaceID: "w1", Provider: "notion", Status: StatusActive, VaultID: "v-1"})
	if err := s.VaultAttachConnector("alice", "v-1", "c1"); err != nil {
		t.Fatal(err)
	}
	if err := s.VaultAttachConnector("alice", "v-2", "c1"); err == nil {
		t.Fatal("a connection that belongs to another vault must not be taken over")
	}
	// Only the owner of the connection's vault may change or remove it (the Vault connector routes check this): another
	// person, or the same person through another vault, cannot.
	if !s.VaultConnectorOwnedBy("alice", "v-1", "c1") || s.VaultConnectorOwnedBy("bob", "v-1", "c1") || s.VaultConnectorOwnedBy("alice", "v-2", "c1") {
		t.Fatal("someone other than the vault's owner could change or remove its connection")
	}
	s.AddConnector(Connector{ID: "c2", WorkspaceID: "w1", Provider: "linear", Status: StatusActive})
	if err := s.VaultAttachConnector("alice", "v-1", "c2"); err == nil {
		t.Fatal("a platform connection must not be taken over")
	}
	// A vault's connection and secret are never granted to the whole company, not even by the startup backfill.
	if err := s.EnsurePlatformGroup("w1"); err != nil {
		t.Fatal(err)
	}
	if err := s.VaultAddSecret("alice", "v-1", "VLT_X__KEY"); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsurePlatformGroup("w1"); err != nil {
		t.Fatal(err)
	}
	pid := PlatformGroupID("w1")
	// A vault connection that was granted to everyone before it was bound to its vault is repaired at start.
	s.mu.Lock()
	s.groupServers[pid]["c1"] = true
	s.mu.Unlock()
	if err := s.EnsurePlatformGroup("w1"); err != nil {
		t.Fatal(err)
	}
	if s.groupServers[pid]["c1"] || s.secretGrants[pid]["VLT_X__KEY"] {
		t.Fatal("a vault's connection or secret must not reach the Platform group")
	}
	if !s.groupServers["v-1"]["c1"] || !s.secretGrants["v-1"]["VLT_X__KEY"] {
		t.Fatal("the vault's own members must have them")
	}
	if got := s.VaultsFor("w1", "carol"); len(got) != 0 {
		t.Fatalf("a stranger sees no vaults, got %d", len(got))
	}
}

func TestProjectVaultPreservesProjectIsolationAndOwnership(t *testing.T) {
	s := NewMemoryStore()
	s.AddWorkspace(Workspace{ID: "w"})
	s.AddUser(User{ID: "owner", WorkspaceID: "w"})
	s.AddUser(User{ID: "other", WorkspaceID: "w"})
	if err := s.EnsureProjectVault("w", "owner", "v-12345678", "Workflow/example", "Example"); err != nil {
		t.Fatal(err)
	}
	if s.VaultOwnedBy("other", "v-12345678") {
		t.Fatal("project secret scope exposed to another person")
	}
	if err := s.VaultAddSecret("owner", "v-12345678", "VLT_12345678__KEY"); err != nil {
		t.Fatal(err)
	}
	if got := s.ListSecrets("w", "owner", "", false); len(got) != 0 {
		t.Fatal("project secrets became account-wide grants")
	}
	if s.SetSecretGrants("w", "VLT_12345678__KEY", "owner", []string{"v-12345678"}, true) == nil {
		t.Fatal("project scope could be granted outside native project access")
	}
	if err := s.EnsureProjectVault("w", "other", "v-12345678", "Workflow/example", "Example"); err != nil {
		t.Fatal(err)
	}
	if s.VaultOwnedBy("owner", "v-12345678") || !s.VaultOwnedBy("other", "v-12345678") {
		t.Fatal("old owner retained secret access after transfer")
	}
	if s.EnsureProjectVault("w", "owner", "v-12345678", "Workflow/different", "Different") == nil {
		t.Fatal("different project reused secret scope")
	}
}

// The one-off conversion (PLAT-856-400): a solely owned vault merges into the owner's personal vault, a shared one
// becomes a group whose connection and secret still reach only its members (never everyone), and a bad plan changes
// nothing.
func TestConvertPersonVaults(t *testing.T) {
	s := NewMemoryStore()
	s.AddWorkspace(Workspace{ID: "w", Name: "w"})
	for _, u := range []string{"alice", "bob"} {
		s.AddUser(User{ID: u, WorkspaceID: "w"})
	}
	if err := s.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.groups["v-old"] = Group{ID: "v-old", WorkspaceID: "w", Name: "Tools", Kind: GroupKindVault, Owners: []string{"alice"}}
	s.members["v-old"] = map[string]bool{"alice": true}
	s.groups["v-team"] = Group{ID: "v-team", WorkspaceID: "w", Name: "Team", Kind: GroupKindVault, Owners: []string{"alice"}}
	s.members["v-team"] = map[string]bool{"alice": true, "bob": true}
	s.mu.Unlock()
	s.AddConnector(Connector{ID: "c-old", WorkspaceID: "w", Provider: "notion", Status: StatusActive, VaultID: "v-old"})
	s.AddConnector(Connector{ID: "c-team", WorkspaceID: "w", Provider: "jira", Status: StatusActive, VaultID: "v-team"})
	for vault, secret := range map[string]string{"v-old": "VLT_OLD__KEY", "v-team": "VLT_TEAM__TOKEN"} {
		if err := s.VaultAddSecret("alice", vault, secret); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ConvertPersonVaults("w", []VaultConversion{{VaultID: "v-team", Mode: "company"}}); err == nil {
		t.Fatal("a plan that leaves a secret without its new name must be refused")
	}
	if _, ok := s.groups["v-team"]; !ok || !s.groups["v-team"].IsVault() {
		t.Fatal("a refused plan changed the store")
	}
	plan := []VaultConversion{
		{VaultID: "v-old", Mode: "merge", TargetID: "v-mine", SecretRenames: map[string]string{"VLT_OLD__KEY": "VLT_MINE__KEY"}},
		{VaultID: "v-team", Mode: "company", SecretRenames: map[string]string{"VLT_TEAM__TOKEN": "TOKEN"}},
	}
	if err := s.ConvertPersonVaults("w", plan); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.groups["v-old"]; ok || s.connectors["c-old"].VaultID != "v-mine" || !s.VaultConnectorOwnedBy("alice", "v-mine", "c-old") || s.secretResources["VLT_MINE__KEY"].VaultID != "v-mine" {
		t.Fatal("the solely owned vault did not merge into the personal vault")
	}
	if mine, _ := s.EnsurePersonalVault("w", "alice", "v-new"); mine != "v-mine" {
		t.Fatalf("the merged personal vault is not the one used afterwards: %s", mine)
	}
	team := s.groups["v-team"]
	if team.IsVault() || len(team.Owners) != 0 || !s.members["v-team"]["bob"] || s.connectors["c-team"].VaultID != "" || !s.groupServers["v-team"]["c-team"] || !s.secretGrants["v-team"]["TOKEN"] {
		t.Fatal("the shared vault did not become a group with the same members, connection and secret")
	}
	if err := s.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	pid := PlatformGroupID("w")
	if s.groupServers[pid]["c-team"] || s.secretGrants[pid]["TOKEN"] {
		t.Fatal("a converted group's connection or secret reached everyone")
	}
}
