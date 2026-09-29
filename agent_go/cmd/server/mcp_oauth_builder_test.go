package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
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

func TestMCPOAuthBuilderConsentBoundsSurviveRefreshAndRestart(t *testing.T) {
	f := newExternalToolsFixture(t)
	builderOAuthSetup(t)
	ctx := context.Background()
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if store != nil {
			store.Close()
		}
	}()
	client, err := store.RegisterClient(ctx, "Builder client", []string{"https://client.example/callback"})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("a", 43)
	sum := sha256.Sum256([]byte(verifier))
	req := mcpOAuthRequest{ClientID: client.ID, RedirectURI: client.RedirectURIs[0], Resource: "https://agentworks.example.com" + externalMCPPath, State: "state", Scopes: builderConsentScopes, Challenge: base64.RawURLEncoding.EncodeToString(sum[:]), ExpiresAt: time.Now().Add(time.Minute)}
	raw, err := store.SaveRequest(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	reader := &UserClaims{UserID: "reader", Username: "reader"}
	for _, tc := range []struct {
		user *UserClaims
		ids  []string
	}{{owner, nil}, {owner, []string{"secret"}}, {reader, []string{"invoices"}}, {owner, []string{"invoices", "invoices"}}} {
		if _, _, err := store.Decide(ctx, raw, tc.user, true, tc.ids); err == nil {
			t.Fatalf("unauthorized bounds approved: %v %v", tc.user.UserID, tc.ids)
		}
	}
	// Rejected submissions leave the consent request available for a valid choice.
	w := httptest.NewRecorder()
	f.api.handleMCPOAuthConsent(w, adminRequest("GET", mcpOAuthConsentPath+"?request="+raw, "", owner, nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "invoices") || strings.Contains(w.Body.String(), "Private research") {
		t.Fatalf("consent choices: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	f.api.handleMCPOAuthConsent(w, adminRequest("POST", mcpOAuthConsentPath+"?request="+raw, `{"decision":"approve","workflow_ids":["invoices"]}`, owner, nil))
	if w.Code != 200 {
		t.Fatalf("consent: %d %s", w.Code, w.Body)
	}
	var result map[string]string
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	redirect, err := url.Parse(result["redirect_url"])
	if err != nil {
		t.Fatal(err)
	}
	grant, access, refresh, err := store.ExchangeCode(ctx, redirect.Query().Get("code"), client.ID, req.RedirectURI, req.Resource, verifier)
	if err != nil {
		t.Fatal(err)
	}
	check := func(grant mcpOAuthGrant) {
		t.Helper()
		token := mcpOAuthTokenForGrant(grant)
		if !token.BuilderAccess() || token.AllWorkflows || !token.AllowsWorkflow("invoices") || token.AllowsWorkflow("secret") {
			t.Fatalf("bounds changed: %+v", token)
		}
	}
	check(grant)
	authenticated, err := store.Authenticate(ctx, access)
	if err != nil {
		t.Fatal(err)
	}
	check(authenticated)
	store.Close()
	store, err = openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	refreshed, _, _, err := store.Refresh(ctx, refresh, client.ID, req.Resource)
	if err != nil {
		t.Fatal(err)
	}
	check(refreshed)
	if refreshed.FamilyID != grant.FamilyID {
		t.Fatal("refresh changed family binding")
	}
	connections, err := store.Connections(ctx, "owner")
	if err != nil || len(connections) != 1 || connections[0].AllWorkflows || !slices.Equal(connections[0].WorkflowIDs, []string{"invoices"}) {
		t.Fatalf("connection bounds: %+v %v", connections, err)
	}
	if err := store.RevokeFamily(ctx, grant.FamilyID, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActiveFamily(ctx, grant.FamilyID); err == nil {
		t.Fatal("revoked Builder family remained active")
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
