package server

import "testing"

func TestProductEnabled(t *testing.T) {
	t.Setenv("AGENT_PRODUCTS", "")
	if !productEnabled("video-studio") || !productEnabled("dominion") {
		t.Fatal("an unset product allowlist must preserve shared-server behavior")
	}

	t.Setenv("AGENT_PRODUCTS", " video-studio, Finance ")
	if !productEnabled("video-studio") || !productEnabled("finance") {
		t.Fatal("configured products must be enabled case-insensitively")
	}
	if productEnabled("dominion") {
		t.Fatal("unlisted product must not be registered")
	}
	// Vault and Brain are core: no allowlist turns them off.
	if !productEnabled("mcp-gateway") || !productEnabled("knowledgebase") {
		t.Fatal("Vault and Brain must be enabled whatever AGENT_PRODUCTS lists")
	}
	for _, own := range []string{"sparkquill", "dominion,work"} {
		t.Setenv("AGENT_PRODUCTS", own)
		if productEnabled("mcp-gateway") || productEnabled("knowledgebase") {
			t.Fatalf("%s is its own product and keeps its allowlist", own)
		}
	}
}

func TestIsSingleProductServerDeployment(t *testing.T) {
	t.Setenv("AGENT_PRODUCTS", "")
	if isSingleProductServerDeployment() {
		t.Fatal("unset AGENT_PRODUCTS is the shared desktop/multi-product server, not a dedicated single-product deployment")
	}

	t.Setenv("AGENT_PRODUCTS", "dominion")
	if !isSingleProductServerDeployment() {
		t.Fatal("a single configured product must count as a dedicated single-product deployment")
	}

	t.Setenv("AGENT_PRODUCTS", "video-studio,finance")
	if isSingleProductServerDeployment() {
		t.Fatal("more than one configured product is not a single-product deployment")
	}

	t.Setenv("AGENT_PRODUCTS", " , ")
	if isSingleProductServerDeployment() {
		t.Fatal("a value with no real product names must not count as configured")
	}
}
