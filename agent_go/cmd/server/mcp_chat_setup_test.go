package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	events "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/oauth"
)

func TestMCPChatAPIKeyIsSealedAndKeepsTheExactAccount(t *testing.T) {
	withMCPConnectionsRoot(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]}]}`)
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AUTH_SECRET", strings.Repeat("s", 32))
	saved, err := addPlaceMCPServer("owner", placeMCPServer{Name: "docs_account", Label: "My docs", URL: "https://provider.example/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{logger: loggerv2.NewNoop()}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	credential := "test-key-never-echo-this-value"
	result, err := api.mcpConnectionTool(ctx, "owner", "install_mcp_server", map[string]interface{}{"name": saved.Name, "api_key": credential, "api_key_header": "X-Api-Key", "api_key_format": "{}"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, credential) || !strings.Contains(result, "discovery") {
		t.Fatal("reply leaked a value or claimed unverified connectivity")
	}
	accounts, err := listPlaceMCPServers("owner")
	if err != nil || len(accounts) != 1 || accounts[0].Name != saved.Name || accounts[0].Label != saved.Label {
		t.Fatal("setup duplicated or replaced the account")
	}
	ref := accounts[0].Headers["X-Api-Key"]
	if ref.Secret == "" || ref.Format != "{}" {
		t.Fatal("provider header was not retained as a secret reference")
	}
	if value, err := personalSecretValue("owner", ref.Secret); err != nil || value != credential {
		t.Fatal("key was not saved for its owner")
	}
	if _, err := personalSecretValue("other", ref.Secret); err == nil {
		t.Fatal("another person could read the key")
	}
	dir, _ := placeMCPDir("owner")
	for _, name := range []string{"servers.json", "secrets.json"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || strings.Contains(string(body), credential) {
			t.Fatal("credential was not sealed at rest")
		}
	}
	if _, err := api.mcpConnectionTool(ctx, "owner", "install_mcp_server", map[string]interface{}{"name": saved.Name, "api_key": credential, "api_key_header": "Bad\nHeader"}); err == nil {
		t.Fatal("unsafe header accepted")
	}
}

func TestMCPChatOAuthAppDetailsFinishThePendingAccountWithoutUISetup(t *testing.T) {
	withMCPConnectionsRoot(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]}]}`)
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AUTH_SECRET", strings.Repeat("s", 32))
	t.Setenv("PUBLIC_URL", "https://agentworks.example")
	saved, err := addPlaceMCPServer("owner", placeMCPServer{Name: "mail_account", URL: "https://provider.example/mcp", OAuth: &oauth.OAuthConfig{AuthURL: "https://provider.example/authorize", TokenURL: "https://provider.example/token"}})
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{logger: loggerv2.NewNoop(), eventStore: events.NewEventStore(10)}
	defer api.eventStore.Stop()
	api.eventStore.SetSessionOwner("owner-chat", "owner")
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	ctx = context.WithValue(ctx, common.ChatSessionIDKey, "owner-chat")
	first, err := api.mcpConnectionTool(ctx, "owner", "install_mcp_server", map[string]interface{}{"name": saved.Name})
	if err != nil || !strings.Contains(first, "requires the user's own OAuth app") || !strings.Contains(first, "omit label and catalog") {
		t.Fatalf("missing guided app setup: %v", err)
	}
	secret := "test-oauth-secret-never-echo"
	result, err := api.mcpConnectionTool(ctx, "owner", "install_mcp_server", map[string]interface{}{"name": saved.Name, "client_id": "test-app-id", "client_secret": secret})
	if err != nil || strings.Contains(result, secret) {
		t.Fatalf("could not continue OAuth setup safely: %v", err)
	}
	auth, err := url.Parse(result[strings.LastIndex(result, " ")+1:])
	if err != nil || auth.Host != "provider.example" || auth.Query().Get("client_id") != "test-app-id" {
		t.Fatal("chat did not return the provider's real authorization link")
	}
	oauthFlowsMu.RLock()
	flow := oauthFlows[auth.Query().Get("state")]
	oauthFlowsMu.RUnlock()
	if flow == nil {
		t.Fatal("authorization flow was not registered")
	}
	defer func() { flow.ErrChan <- errors.New("test ended before human consent") }()
	if flow.ServerConfig.OAuth.ClientSecret != secret {
		t.Fatal("app secret was not supplied to the authorization flow")
	}
	accounts, _ := listPlaceMCPServers("owner")
	if len(accounts) != 1 || accounts[0].Name != saved.Name {
		t.Fatal("pending authorization created another account")
	}
	dir, _ := placeMCPDir("owner")
	body, _ := os.ReadFile(filepath.Join(dir, "servers.json"))
	if strings.Contains(string(body), secret) {
		t.Fatal("app secret was written into server metadata")
	}
	if _, err := os.Stat(placeMCPClientFile(dir, "owner", saved.Name)); !os.IsNotExist(err) {
		t.Fatal("app credentials were retained before successful human authorization")
	}
}

func TestMCPChatCallbackUsesOnlyTheAuthenticatedOwnersRequest(t *testing.T) {
	t.Setenv("PUBLIC_URL", "")
	api := &StreamingAPI{lastQueryRequests: map[string]QueryRequest{"chat": {userID: "owner", oauthRedirectURI: "http://localhost:51733/api/oauth/callback"}}}
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, "chat")
	if got := api.mcpToolOAuthRedirect(ctx, "owner"); got != "http://localhost:51733/api/oauth/callback" {
		t.Fatal("authenticated local callback not available to the Builder")
	}
	if got := api.mcpToolOAuthRedirect(ctx, "other"); got != "" {
		t.Fatal("borrowed another person's callback")
	}
	var request QueryRequest
	if err := json.Unmarshal([]byte(`{"oauthRedirectURI":"https://untrusted.example/callback","oauth_redirect_uri":"https://untrusted.example/callback"}`), &request); err != nil || request.oauthRedirectURI != "" {
		t.Fatal("client JSON controlled the callback address")
	}
	t.Setenv("PUBLIC_URL", "https://configured.example")
	if got := api.mcpToolOAuthRedirect(ctx, "owner"); got != "https://configured.example/api/oauth/callback" {
		t.Fatal("deployment callback did not take precedence")
	}
}
