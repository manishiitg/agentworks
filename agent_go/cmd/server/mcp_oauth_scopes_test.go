package server

import (
	"slices"
	"testing"
)

// A connection never offers or grants Builder editing or Relay authoring on a server that has them off
// (AGENTWORKS_MCP_BUILDER_ENABLED), so a client that requests every advertised scope can still be approved.
func TestMCPOAuthScopesDropBuilderAndRelaysWhereTheServerHasThemOff(t *testing.T) {
	all := slices.Clone(mcpOAuthScopes)
	t.Setenv("AGENTWORKS_MCP_BUILDER_ENABLED", "")
	got := mcpOAuthScopesFor(&UserClaims{}, all)
	for _, scope := range []string{"builder:chat", "relays:write"} {
		if slices.Contains(got, scope) {
			t.Errorf("%s offered on a server with Builder off: %v", scope, got)
		}
	}
	for _, scope := range []string{"workflows:read", "files:read", "runs:execute", "crews:read", "crews:run", "crews:write"} {
		if !slices.Contains(got, scope) {
			t.Errorf("%s was dropped although the server allows it: %v", scope, got)
		}
	}
	t.Setenv("AGENTWORKS_MCP_BUILDER_ENABLED", "true")
	on := mcpOAuthScopesFor(&UserClaims{}, all)
	if !slices.Contains(on, "builder:chat") || !slices.Contains(on, "relays:write") {
		t.Errorf("Builder and Relay scopes must stay where the server enables them: %v", on)
	}
}

// Where Builder is on, Relay authoring is still offered only to accounts that may edit and have the Relays product.
func TestMCPOAuthRelayScopeFollowsTheAccount(t *testing.T) {
	t.Setenv("AGENTWORKS_MCP_BUILDER_ENABLED", "true")
	all := slices.Clone(mcpOAuthScopes)
	got := mcpOAuthScopesFor(&UserClaims{}, all)
	if !slices.Contains(got, "builder:chat") {
		t.Errorf("builder:chat must stay where the server enables it: %v", got)
	}
	if userAllowedProduct(&UserClaims{}, "relays") && userAccessForClaims(&UserClaims{}).CanEdit {
		t.Skip("the zero-value account has Relays here")
	}
	if slices.Contains(got, "relays:write") {
		t.Errorf("an account without the Relays product was offered relays:write: %v", got)
	}
}
