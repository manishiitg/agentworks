package server

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var builderConsentScopes = []string{"workflows:read", "files:read", "runs:execute", "builder:chat"}

func builderOAuthSetup(t *testing.T) {
	t.Helper()
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	t.Setenv("AUTH_SECRET", "builder-mcp-oauth-test-signing-secret")
	t.Setenv("PUBLIC_URL", "https://agentworks.example.com")
	// These tests are about servers with Builder switched on; a server with it off never offers the scope.
	t.Setenv("AGENTWORKS_MCP_BUILDER_ENABLED", "true")
}

func TestMCPOAuthBuilderIsExplicitAndCLIDefaultsUnchanged(t *testing.T) {
	scopes, ok := validMCPOAuthScopes("")
	if !ok || !slices.Equal(scopes, mcpOAuthDefaultScopes) || slices.Contains(scopes, "builder:chat") {
		t.Fatalf("default scopes widened: %v", scopes)
	}
	if _, ok := validMCPOAuthScopes("builder:chat"); ok {
		t.Fatal("missing Builder companion scopes accepted")
	}
	if _, ok := validMCPOAuthScopes(strings.Join(builderConsentScopes, " ")); !ok {
		t.Fatal("explicit Builder scope rejected")
	}
	builderOAuthSetup(t)
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, verification, err := store.CreateCLIDevice(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	scopes, err = store.CLIDeviceRequest(context.Background(), verification)
	if err != nil || !slices.Equal(scopes, mcpOAuthDefaultScopes) {
		t.Fatalf("CLI default changed: %v %v", scopes, err)
	}
}

func TestMCPOAuthBuilderSelectionCannotAddUnrequestedPermission(t *testing.T) {
	if err := validateMCPOAuthBuilderSelection(context.Background(), &UserClaims{UserID: "owner"}, mcpOAuthDefaultScopes, []string{"invoices"}); err == nil {
		t.Fatal("unrequested selection accepted")
	}
}

func TestMCPOAuthMigratesLegacyUnboundedGrants(t *testing.T) {
	builderOAuthSetup(t)
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	path, err := mcpOAuthSecret()
	if err != nil {
		t.Fatal(err)
	}
	// Create the old schema using a normal store, then remove the new column.
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.db.Exec(`ALTER TABLE tokens DROP COLUMN workflow_ids`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.db.Exec(`ALTER TABLE codes DROP COLUMN workflow_ids`)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	raw := mcpOAuthAccessPrefix + strings.Repeat("b", 64)
	_, err = old.Exec(`INSERT INTO tokens(hash,kind,family_id,client_id,resource,scopes,user_id,username,email,provider,expires_at) VALUES (?,'access','family','client','resource','["workflows:read","runs:execute"]','owner','owner','','',?)`, mcpOAuthHash(raw), time.Now().Add(time.Hour).Unix())
	old.Close()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		store, err = openMCPOAuthStore()
		if err != nil {
			t.Fatal(err)
		}
		grant, err := store.Authenticate(context.Background(), raw)
		store.Close()
		if err != nil {
			t.Fatal(err)
		}
		token := mcpOAuthTokenForGrant(grant)
		if !token.AllWorkflows || token.BuilderAccess() || token.Allows("builder:chat") {
			t.Fatalf("legacy grant authority changed: %+v", token)
		}
	}
}
