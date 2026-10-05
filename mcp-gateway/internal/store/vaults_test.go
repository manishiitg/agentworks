package store

import (
	"fmt"
	"strings"
	"testing"
)

// Pins the owner decisions for person-owned vaults (PLAT-507): at most 5 owned per person, only an owner changes
// members, owners or connections, a vault keeps at least one owner, and deleting needs it empty.
func TestVaultOwnershipRules(t *testing.T) {
	s := NewMemoryStore()
	s.AddUser(User{ID: "alice", WorkspaceID: "w1", Email: "alice@example.com"})
	s.AddUser(User{ID: "bob", WorkspaceID: "w1", Email: "bob@example.com"})
	s.AddUser(User{ID: "carol", WorkspaceID: "w1", Email: "carol@example.com"})

	for i := 0; i < MaxVaultsPerOwner; i++ {
		if err := s.CreateVault("w1", "alice", fmt.Sprintf("v-%d", i), "Vault", ""); err != nil {
			t.Fatalf("vault %d: %v", i, err)
		}
	}
	if err := s.CreateVault("w1", "alice", "v-extra", "Vault", ""); err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("a sixth owned vault must be refused, got %v", err)
	}

	if err := s.VaultSetMember("bob", "v-0", "carol", true); err == nil {
		t.Fatal("a non-owner must not add members")
	}
	if err := s.VaultSetMember("alice", "v-0", "bob", true); err != nil {
		t.Fatal(err)
	}
	if err := s.VaultSetMember("bob", "v-0", "carol", true); err == nil {
		t.Fatal("a member must not re-share")
	}
	if err := s.VaultSetOwner("alice", "v-0", "alice", false); err == nil {
		t.Fatal("the last owner must not be removable")
	}
	if err := s.VaultSetOwner("alice", "v-0", "bob", true); err != nil {
		t.Fatal(err)
	}
	if err := s.VaultSetOwner("bob", "v-0", "alice", false); err != nil {
		t.Fatalf("a second owner can remove the first: %v", err)
	}
	if !s.VaultOwnedBy("bob", "v-0") || s.VaultOwnedBy("alice", "v-0") {
		t.Fatal("ownership did not move")
	}

	s.AddConnector(Connector{ID: "c1", WorkspaceID: "w1", Provider: "notion", Status: StatusActive})
	if err := s.VaultAttachConnector("alice", "v-1", "c1"); err != nil {
		t.Fatal(err)
	}
	if err := s.VaultAttachConnector("alice", "v-2", "c1"); err == nil {
		t.Fatal("a connection already in a vault must not be taken over")
	}
	if err := s.DeleteVault("alice", "v-1"); err == nil {
		t.Fatal("a vault with connections must not be deleted")
	}
	if got := s.VaultsFor("w1", "carol"); len(got) != 0 {
		t.Fatalf("a stranger sees no vaults, got %d", len(got))
	}
}
