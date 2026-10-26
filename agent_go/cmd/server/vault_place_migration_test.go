package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
	oauth2 "golang.org/x/oauth2"
)

// The one-off move of the old project-connection store into Vault: a dry run says what each attachment would do, and
// changes nothing. Catalog/custom OAuth, key/header and no-auth records all move.
func TestPlaceMigrationDryRunClassifiesAndChangesNothing(t *testing.T) {
	withMCPConnectionsRoot(t)
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{}})
	t.Cleanup(workspace.Close)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AUTH_SECRET", "place-migration-test-secret-with-enough-entropy")
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/connectors" {
			json.NewEncoder(w).Encode(map[string]any{"connectors": []any{}})
			return
		}
		if r.URL.Path != "/api/admin/catalog" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"providers": []any{
			map[string]any{"Name": "Slack", "URL": "https://mcp.slack.com/mcp"},
			map[string]any{"Name": "Notion", "URL": "https://mcp.notion.com/mcp"},
			map[string]any{"Name": "Context7", "URL": "https://mcp.context7.com/mcp"},
		}})
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)
	signIn := &oauth.OAuthConfig{AuthURL: "https://idp.example.com/authorize", TokenURL: "https://idp.example.com/token"}
	servers := []placeMCPServer{
		{Name: "slack_work", Label: "Work Slack", Catalog: "Slack", URL: "https://mcp.slack.com/mcp", Transport: "http", OAuth: &oauth.OAuthConfig{AuthURL: "https://slack.com/oauth/v2_user/authorize", TokenURL: "https://slack.com/api/oauth.v2.user.access"}},
		{Name: "notion", Label: "Notion", Catalog: "Notion", URL: "https://mcp.notion.com/mcp", Transport: "http", OAuth: signIn},
		{Name: "jam", Label: "Jam", URL: "https://mcp.jam.example.com/mcp", Transport: "http", OAuth: signIn},
		{Name: "crm", Label: "CRM", URL: "https://crm.example.com/mcp", Transport: "http", Headers: map[string]placeMCPHeader{"Authorization": {Secret: "CRM_TOKEN", Format: "Bearer {}"}}},
		{Name: "context7", Label: "Context7", URL: "https://mcp.context7.com/mcp", Transport: "http"},
		{Name: "gmail", Label: "Gmail", Catalog: "GoogleGmail", URL: "https://gmailmcp.googleapis.com/mcp/v1", Transport: "http", OAuth: signIn},
	}
	for _, server := range servers {
		if _, err := addPlaceMCPServer("alice", server); err != nil {
			t.Fatalf("%s: %v", server.Name, err)
		}
		if err := attachPersonalLoginToPlace("alice", server.Name, "Workflow/orders"); err != nil {
			t.Fatal(err)
		}
	}
	api := &StreamingAPI{}
	rows, err := api.migratePlaceConnections(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, row := range rows {
		got[row.Server] = row.Kind + ":" + row.Result
	}
	want := map[string]string{"slack_work": "slack:would move", "notion": "oauth:would move", "jam": "oauth:would move", "crm": "key:would move", "context7": "noauth:would move", "gmail": "oauth:would move"}
	for server, result := range want {
		if got[server] != result {
			t.Fatalf("%s = %q, want %q (all: %v)", server, got[server], result, got)
		}
	}
	if attached, _ := placeMCPAttachmentsFor("Workflow/orders"); len(attached) != len(servers) {
		t.Fatalf("a dry run changed the attachments: %+v", attached)
	}
}

// PLAT-856-370: copied credentials were intact, but an empty saved resource
// failed the strict reader before the existing account could refresh.
func TestImportedOAuthResourceRepairPreservesLoginAndRejectsMismatches(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AUTH_SECRET", strings.Repeat("k", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	oauth.SetTokenSealer(platformClientSealer{})
	t.Cleanup(func() { oauth.SetTokenSealer(nil) })
	const good, mismatch, unrelated, endpointMismatch = "c-11111111", "c-22222222", "c-33333333", "c-44444444"
	const upstream = "https://example.com/mcp"
	var refreshes atomic.Int32
	var rejectRefresh atomic.Bool
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshes.Add(1)
		if rejectRefresh.Load() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"invalid_grant","error_description":"private-provider-text"}`))
			return
		}
		r.ParseForm()
		client, secret, basic := r.BasicAuth()
		if !basic || client != "existing-client" || secret != "existing-client-secret" {
			t.Error("repair changed the account's client registration")
			w.WriteHeader(401)
			return
		}
		if r.Form.Get("refresh_token") != "existing-refresh" {
			t.Error("repair changed the account's refresh identity")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"refreshed-existing-account","token_type":"Bearer","refresh_token":"existing-refresh","expires_in":3600}`))
	}))
	t.Cleanup(issuer.Close)
	canonical := oauth.OAuthConfig{AuthURL: issuer.URL + "/authorize", TokenURL: issuer.URL + "/token", Resource: upstream}
	connections := map[string]vaultOAuthConnection{}
	for _, id := range []string{good, mismatch, unrelated, endpointMismatch} {
		connections[id] = vaultOAuthConnection{ID: id, OAuthServer: "Test", OAuthCredentialID: id, UpstreamURL: upstream, Status: "active"}
	}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/connectors" {
			json.NewEncoder(w).Encode(map[string]any{"connectors": []any{
				map[string]any{"ID": good, "InstanceSlug": "migrated-one", "OAuthServer": "Test"},
				map[string]any{"ID": mismatch, "InstanceSlug": "migrated-two", "OAuthServer": "Test"},
				map[string]any{"ID": unrelated, "InstanceSlug": "ordinary-account", "OAuthServer": "Test"},
				map[string]any{"ID": endpointMismatch, "InstanceSlug": "migrated-three", "OAuthServer": "Test"},
			}})
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/admin/connectors/")
		if c, ok := connections[id]; ok {
			json.NewEncoder(w).Encode(c)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(gateway.Close)
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)
	configPath := filepath.Join(t.TempDir(), "mcp.json")
	if err := mcpclient.SaveConfig(configPath, &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{"Test": {URL: upstream, OAuth: &canonical}}}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{mcpConfigPath: configPath, logger: loggerv2.NewNoop()}
	for _, id := range []string{good, mismatch, unrelated, endpointMismatch} {
		saved := canonical
		saved.Resource = ""
		if id == mismatch {
			saved.Resource = "https://other.example.com/mcp"
		}
		if id == endpointMismatch {
			saved.TokenURL = issuer.URL + "/different"
		}
		saved.ClientID, saved.ClientSecret = "existing-client", "existing-client-secret"
		saved.TokenFile = getUserTokenFilePath(platformMCPTokenUserID, vaultCredentialName(id))
		encoded, _ := json.Marshal(saved)
		if err := oauth.WriteTokenFile(vaultCredentialConfigPath(id), encoded); err != nil {
			t.Fatal(err)
		}
	}
	tokenPath := getUserTokenFilePath(platformMCPTokenUserID, vaultCredentialName(good))
	if err := oauth.NewTokenStore(tokenPath).Save(&oauth2.Token{AccessToken: "expired-existing-account", RefreshToken: "existing-refresh", Expiry: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(vaultCredentialConfigPath(good))
	tokenBefore, _ := os.ReadFile(tokenPath)
	if _, err := api.vaultOAuthConfig(t.Context(), good, "Test", upstream); err == nil {
		t.Fatal("strict reader accepted missing resource before the migration repair")
	}
	status, err := api.capLayerConnectionAccess(t.Context(), "default", "connection_status", json.RawMessage(`{"connection_id":"c-11111111"}`))
	if err != nil || !strings.Contains(status, "setup") || refreshes.Load() != 0 {
		t.Fatalf("setup failure was hidden or attempted a refresh: err=%v calls=%d", err, refreshes.Load())
	}
	if _, err := api.normalizeMigratedOAuthResources(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	preview, _ := os.ReadFile(vaultCredentialConfigPath(good))
	if !bytes.Equal(before, preview) {
		t.Fatal("preview changed saved OAuth setup")
	}
	if _, err := api.normalizeMigratedOAuthResources(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := api.vaultOAuthConfig(t.Context(), good, "Test", upstream); err != nil {
		t.Fatalf("repaired setup does not pass the unchanged canonical reader: %v", err)
	}
	for _, id := range []string{mismatch, unrelated, endpointMismatch} {
		if _, err := api.vaultOAuthConfig(t.Context(), id, "Test", upstream); err == nil {
			t.Fatal("repair changed an explicit mismatch or an unrelated account")
		}
	}
	state, _ := workflowCLIStateRoot()
	backups, _ := filepath.Glob(filepath.Join(state, "migration-backups", "vault-oauth-resource-*", good+".json"))
	if len(backups) != 1 {
		t.Fatal("repair did not preserve the encrypted setup first")
	}
	backup, _ := os.ReadFile(backups[0])
	afterToken, _ := os.ReadFile(tokenPath)
	if !bytes.Equal(before, backup) || !bytes.Equal(tokenBefore, afterToken) || refreshes.Load() != 0 {
		t.Fatal("repair changed credentials or failed to back up their original setup")
	}
	status, err = api.capLayerConnectionAccess(t.Context(), "default", "connection_status", json.RawMessage(`{"connection_id":"c-11111111"}`))
	if err != nil || !strings.Contains(status, `"authenticated":true`) || refreshes.Load() == 0 || strings.Contains(status, "refreshed-existing-account") {
		t.Fatal("normal status did not refresh the same account without returning its token", err)
	}
	rejectRefresh.Store(true)
	if err := oauth.NewTokenStore(tokenPath).Save(&oauth2.Token{AccessToken: "expired", RefreshToken: "existing-refresh", Expiry: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	status, err = api.capLayerConnectionAccess(t.Context(), "default", "connection_status", json.RawMessage(`{"connection_id":"c-11111111"}`))
	if err != nil || !strings.Contains(status, "Sign-in needed:") || strings.Contains(status, "private-provider-text") || strings.Contains(status, "existing-refresh") {
		t.Fatal("refresh failure was hidden or leaked the provider response", err)
	}
}
