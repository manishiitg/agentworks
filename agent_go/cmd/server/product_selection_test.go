package server

import "testing"

func TestRegisteredProductIDsExcludeRetiredProduct(t *testing.T) {
	for _, configured := range []string{"dominion", "dominion,agentworks", "retired-product", "retired-product,agentworks"} {
		t.Setenv("AGENT_PRODUCTS", configured)
		products := registeredProductIDs()
		for _, product := range products {
			if product == "dominion" || product == "retired-product" {
				t.Fatalf("retired product is advertised: %v", products)
			}
		}
		if !productEnabled("knowledgebase") || !productEnabled("mcp-gateway") {
			t.Fatal("a stale retired product setting must not disable core products")
		}
	}
}

func TestProductEnabled(t *testing.T) {
	t.Setenv("AGENT_PRODUCTS", "")
	if !productEnabled("example-product") || !productEnabled("retired-product") {
		t.Fatal("an unset product allowlist must preserve shared-server behavior")
	}

	t.Setenv("AGENT_PRODUCTS", " example-product, Finance ")
	if !productEnabled("example-product") || !productEnabled("finance") {
		t.Fatal("configured products must be enabled case-insensitively")
	}
	if productEnabled("retired-product") {
		t.Fatal("unlisted product must not be registered")
	}
	// Vault and Brain are core: no allowlist turns them off.
	if !productEnabled("mcp-gateway") || !productEnabled("knowledgebase") {
		t.Fatal("Vault and Brain must be enabled whatever AGENT_PRODUCTS lists")
	}
}

func TestIsSingleProductServerDeployment(t *testing.T) {
	t.Setenv("AGENT_PRODUCTS", "")
	if isSingleProductServerDeployment() {
		t.Fatal("unset AGENT_PRODUCTS is the shared desktop/multi-product server, not a dedicated single-product deployment")
	}

	t.Setenv("AGENT_PRODUCTS", "retired-product")
	if !isSingleProductServerDeployment() {
		t.Fatal("a single configured product must count as a dedicated single-product deployment")
	}

	t.Setenv("AGENT_PRODUCTS", "example-product,finance")
	if isSingleProductServerDeployment() {
		t.Fatal("more than one configured product is not a single-product deployment")
	}

	t.Setenv("AGENT_PRODUCTS", " , ")
	if isSingleProductServerDeployment() {
		t.Fatal("a value with no real product names must not count as configured")
	}
}
