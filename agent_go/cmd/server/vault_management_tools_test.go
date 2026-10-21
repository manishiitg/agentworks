package server

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
)

// The server refuses to start when the Vault tools in code and in product.yaml differ in name or ORDER
// (registerVaultManagementTools pairs them by position). A new Vault tool must be added to both in the same place; this
// fails here instead of on a live server.
func TestVaultManagementToolsMatchProductYAMLInOrder(t *testing.T) {
	definitions := vaultManagementDefinitions()
	names := caplayerproduct.ExternalTools()
	if len(definitions) != len(names) {
		t.Fatalf("%d tools in code, %d in product.yaml", len(definitions), len(names))
	}
	if bindings := caplayerproduct.Manifest().Profile.Tools; len(bindings) != len(definitions) {
		t.Fatalf("%d tool bindings in product.yaml, %d tools in code", len(bindings), len(definitions))
	}
	for i, definition := range definitions {
		if definition.Name != names[i] {
			t.Fatalf("position %d: code has %q, product.yaml has %q", i, definition.Name, names[i])
		}
	}
}
