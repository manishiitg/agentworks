package server

import (
	"testing"
)

// The same provider is not an identity: never auto-attach its alternative
// account. A previously selected server account remains the owner's choice.
func TestCrewDependencyConnectionMatchNeedsExactIdentity(t *testing.T) {
	// PR621's real finding: an unavailable Vault list does not prevent
	// cleanup of independently listed personal Google accounts.
	partial := projectVaultInventory{Notices: []string{"Vaults could not be loaded. Ask in chat to check the connection."}}
	if crewDependencyInventoryUnavailable(partial, "google") || !crewDependencyInventoryUnavailable(partial, "mcp") {
		t.Fatal("unrelated inventory failure blocks personal account cleanup")
	}

	inventory := projectVaultInventory{Vaults: []projectVaultGroup{{Items: []projectVaultItem{{ID: "server", Kind: "mcp", Name: "Other account", Provider: "example", CanSelect: true}, {ID: "private", Kind: "mcp", Name: "Local account", Provider: "example"}}}}}
	want := crewDependencyConnection{Kind: "mcp", Name: "Local account", Provider: "example"}
	if item, exact := matchCrewConnection(want, inventory); item == nil || exact {
		t.Fatal("provider-only match treated as exact")
	}
	inventory.Vaults[0].Items[0].Name = want.Name
	if item, exact := matchCrewConnection(want, inventory); item == nil || !exact {
		t.Fatal("explicit admitted account not matched")
	}
	want.AccountEmail = "owner@example.com"
	inventory.Vaults[0].Items[0].AccountEmail = "other@example.com"
	if item, _ := matchCrewConnection(want, inventory); item != nil {
		t.Fatal("another account email matched")
	}
}
