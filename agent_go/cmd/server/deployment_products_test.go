package server

import (
	"strings"
	"testing"
)

func TestLocalInstallationExcludesServerProducts(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "0")
	t.Setenv("AGENT_PRODUCTS", "agentworks,work,code,relays,knowledgebase,mcp-gateway,llm-gateway")
	t.Setenv("CAPLAYER_SERVICE_URL", "http://127.0.0.1:18745")
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("x", 32))
	for _, product := range []string{"code", "relays", "knowledgebase", "mcp-gateway", "llm-gateway", " MCP-GATEWAY "} {
		if productEnabled(product) || userAllowedProduct(nil, product) || userAllowedProduct(&UserClaims{UserID: "default"}, product) {
			t.Fatalf("local installation admits excluded product %q", product)
		}
	}
	for _, product := range []string{"agentworks", "work"} {
		if !productEnabled(product) || !userAllowedProduct(nil, product) {
			t.Fatalf("local product %q must remain available", product)
		}
	}
	for _, product := range registeredProductIDs() {
		if serverOnlyProduct(product) {
			t.Fatalf("local directory advertises %q", product)
		}
	}
	if _, _, err := capLayerServiceConfig(); err == nil {
		t.Fatal("an inherited Vault endpoint must not enable the local installation")
	}
	if vaultConfigured() || personVaultActive("default") {
		t.Fatal("local installation must not discover shared resources or expose My vaults tools")
	}
	t.Setenv("AGENT_PRODUCTS", "agentworks")
	if isSingleProductServerDeployment() {
		t.Fatal("local installation is not a dedicated server")
	}
}

func TestLocalInstallationServerProductsRequireExplicitOptIn(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("CAPLAYER_SERVICE_URL", "http://127.0.0.1:18745")
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("x", 32))
	for _, value := range []string{"", "0", "false", "true"} {
		t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", value)
		if installationProductAvailable("knowledgebase") || vaultConfigured() {
			t.Fatalf("value %q must not enable local server products", value)
		}
	}
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "1")
	for _, product := range []string{"code", "relays", "knowledgebase", "mcp-gateway", "llm-gateway"} {
		if !productEnabled(product) || !userAllowedProduct(&UserClaims{UserID: "default"}, product) {
			t.Fatalf("explicit local opt-in must admit %q", product)
		}
	}
	if !vaultConfigured() {
		t.Fatal("opted-in source installation must use its configured Vault service")
	}
	if _, _, err := capLayerServiceConfig(); err != nil {
		t.Fatalf("opted-in local Vault config: %v", err)
	}
	t.Setenv("AGENT_PRODUCTS", "code")
	if productEnabled("relays") {
		t.Fatal("local opt-in must not bypass a product allowlist")
	}
	if isSingleProductServerDeployment() {
		t.Fatal("opt-in must not change local installation into a dedicated server")
	}
}

func TestServerInstallationRetainsSharedProducts(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENT_PRODUCTS", "")
	for _, product := range []string{"code", "relays", "knowledgebase", "mcp-gateway", "llm-gateway"} {
		if !productEnabled(product) {
			t.Fatalf("server excludes shared product %q", product)
		}
	}
}
