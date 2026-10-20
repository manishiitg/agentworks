package server

import (
	"slices"
	"testing"
)

// Native CLI/MCP authoring is available even when a legacy deployment flag is off.
// Explicitly narrow grants stay narrow; every call still checks live roles.
func TestMCPOAuthAuthoringDoesNotNeedDeploymentSwitch(t *testing.T) {
	for _, setting := range []string{"", "false", "true"} {
		t.Setenv("AGENTWORKS_MCP_BUILDER_ENABLED", setting)
		got := mcpOAuthScopesFor(&UserClaims{}, slices.Clone(mcpOAuthScopes))
		if !slices.Contains(got, "builder:chat") {
			t.Fatalf("authoring consent hidden by legacy setting %q: %v", setting, got)
		}
		narrow := mcpOAuthScopesFor(&UserClaims{}, []string{"workflows:read"})
		if !slices.Equal(narrow, []string{"workflows:read"}) {
			t.Fatalf("read-only request broadened: %v", narrow)
		}
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

// An administrator who takes the default scopes gets every supported scope the server and account allow (Brain, Builder, Relays,
// dashboards, file writes); an ordinary account keeps the default set, and a client that names its scopes gets exactly those.
func TestAdminDefaultScopesAreEveryScopeTheAccountMayHave(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_MCP_BUILDER_ENABLED", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"root","username":"root","admin":true,"can_create":true,"products":[]},{"id":"member","username":"member","can_create":true,"products":["code"]}]}`)
	admin := &UserClaims{UserID: "root", Username: "root"}
	member := &UserClaims{UserID: "member", Username: "member"}

	adminDefault := mcpOAuthScopesFor(admin, slices.Clone(mcpOAuthDefaultScopes))
	for _, scope := range []string{"builder:chat", "dashboards:read", "knowledgebase:read", "knowledgebase:write"} {
		if !slices.Contains(adminDefault, scope) {
			t.Errorf("an administrator on the default scopes lacks %s: %v", scope, adminDefault)
		}
	}
	// Retiring an extra permission must not turn a legacy explicit request
	// into the special administrator default request and add authoring consent.
	legacyRequested := append(slices.Clone(mcpOAuthDefaultScopes), "files:write", "dashboards:write")
	legacyGranted := mcpOAuthScopesFor(admin, legacyRequested)
	if !slices.Equal(legacyGranted, mcpOAuthDefaultScopes) {
		t.Errorf("legacy request gained permissions: %v", legacyGranted)
	}
	memberDefault := mcpOAuthScopesFor(member, slices.Clone(mcpOAuthDefaultScopes))
	for _, scope := range []string{"builder:chat", "users:manage", "crews:review"} {
		if slices.Contains(memberDefault, scope) {
			t.Errorf("an ordinary account on the default scopes was given %s: %v", scope, memberDefault)
		}
	}
	named := mcpOAuthScopesFor(admin, []string{"workflows:read"})
	if !slices.Equal(named, []string{"workflows:read"}) {
		t.Errorf("a client that names its scopes must get exactly those, got %v", named)
	}
}

// A token never carries Crew scopes for an account without the Crew product (PLAT-820): every Crew tool is refused to it.
func TestMCPOAuthCrewScopesFollowTheCrewProduct(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[
		{"id":"codeonly","username":"codeonly","products":["code"]},
		{"id":"both","username":"both","products":["code","work"]}
	]}`)
	asked := []string{"crews:read", "crews:run", "crews:write", "code:run"}
	got := mcpOAuthScopesFor(&UserClaims{UserID: "codeonly", Username: "codeonly"}, asked)
	if len(got) != 1 || got[0] != "code:run" {
		t.Errorf("a Code-only account must keep only code:run, got %v", got)
	}
	if got := mcpOAuthScopesFor(&UserClaims{UserID: "both", Username: "both"}, asked); len(got) != 4 {
		t.Errorf("an account with both products keeps every scope, got %v", got)
	}
}

// A person's sign-in carries every scope the account may use, worked out from the CURRENT account on every call, whatever was
// stored when they signed in or the app asked for (owner 2026-10-16: the same access as the website). A bound-agent
// connection (messaging only) and a grant that names workflows keep what they were given.
func TestPersonSignInCarriesTheFullScopeSetLive(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"member","username":"member","can_create":true,"products":["code","work"]}]}`)
	person := mcpOAuthTokenForGrant(mcpOAuthGrant{UserID: "member", Username: "member", FamilyID: "f1", Scopes: []string{"workflows:read"}})
	for _, want := range []string{"code:run", "builder:chat", "crews:run", "dashboards:read", "files:read", "runs:execute"} {
		if !slices.Contains(person.Scopes, want) {
			t.Errorf("a person's sign-in stored with one scope lacks %s: %v", want, person.Scopes)
		}
	}
	for _, never := range []string{"users:manage", "vault:manage", "crews:review", "llm:use", "devices:connect"} {
		if slices.Contains(person.Scopes, never) {
			t.Errorf("an ordinary member's sign-in was given %s: %v", never, person.Scopes)
		}
	}
	bound := mcpOAuthTokenForGrant(mcpOAuthGrant{UserID: "member", Username: "member", FamilyID: "f2", Scopes: []string{"crews:run"}, BoundAgentID: "agent-1", BoundAgentName: "helper"})
	if !slices.Equal(bound.Scopes, []string{"crews:run"}) {
		t.Errorf("a bound-agent connection was widened: %v", bound.Scopes)
	}
	named := mcpOAuthTokenForGrant(mcpOAuthGrant{UserID: "member", Username: "member", FamilyID: "f3", Scopes: []string{"workflows:read"}, WorkflowIDs: []string{"w1"}})
	if !slices.Equal(named.Scopes, []string{"workflows:read"}) {
		t.Errorf("a grant that names workflows was widened: %v", named.Scopes)
	}
}
