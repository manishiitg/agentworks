package server

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	events "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpcache"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestMCPToolIdentityUsesSessionOwnerAndRejectsMissingOrConflictingUsers(t *testing.T) {
	api := &StreamingAPI{eventStore: events.NewEventStore(10)}
	api.eventStore.SetSessionOwner("chat-alice", "alice")
	ctx := executor.WithSessionID(context.Background(), "chat-alice")
	if got, err := api.mcpToolUserID(ctx); err != nil || got != "alice" {
		t.Fatalf("bridge identity = %q, %v", got, err)
	}
	for _, bad := range []context.Context{
		context.Background(),
		executor.WithSessionID(context.Background(), "unknown"),
		context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "bob"}),
		context.WithValue(ctx, common.UserIDKey, "bob"),
	} {
		if _, err := api.mcpToolUserID(bad); err == nil {
			t.Fatal("missing/mismatched identity must not fall back to default")
		}
	}
	for _, direct := range []context.Context{
		context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "alice"}),
		context.WithValue(context.Background(), common.UserIDKey, "alice"),
	} {
		if got, err := api.mcpToolUserID(direct); err != nil || got != "alice" {
			t.Fatalf("direct execution identity = %q, %v", got, err)
		}
	}
}

func TestMCPToolListUsesOnlyPrivateDiscoveryCache(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	name := "test-account-connector-" + t.Name()
	cfg := mcpclient.MCPServerConfig{URL: "https://provider.example/mcp", OAuth: &oauth.OAuthConfig{}}
	api := &StreamingAPI{logger: loggerv2.NewNoop()}
	withMCPConnectionsRoot(t)
	_, err := addPlaceMCPServer("alice", placeMCPServer{Name: "private_test", Catalog: name, URL: "https://example.com/mcp", Transport: "http"})
	if err != nil {
		t.Fatal(err)
	}
	internal, owned, err := placeMCPServerConfig("alice", "private_test")
	if err != nil {
		t.Fatal(err)
	}

	cache := mcpcache.GetCacheManager(api.logger)
	entry := &mcpcache.CacheEntry{ServerName: internal, CreatedAt: time.Now(), IsValid: true, TTLMinutes: 30, Tools: []llmtypes.Tool{{Function: &llmtypes.FunctionDefinition{Name: "account_search"}}}}
	if err := cache.Put(entry, owned); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Invalidate(mcpcache.GenerateUnifiedCacheKey(internal, owned)) })
	if status := api.mcpToolStatusForUser(name, "alice", cfg); status.Status != "ok" || len(status.FunctionNames) != 1 {
		t.Fatalf("platform discovery not shown: %+v", status)
	}
	if status := api.mcpToolStatusForUser(name, "bob", cfg); status.Status == "ok" || len(status.FunctionNames) != 0 {
		t.Fatal("Alice's discovery was visible to Bob")
	}
	if cfg.OAuth.TokenFile != "" {
		t.Fatal("status lookup mutated shared OAuth config")
	}
}

func TestMCPInstallToolRejectsAnonymousBridgeBeforeConfigMutation(t *testing.T) {
	api := &StreamingAPI{}
	registrar := &recordingRegistrar{}
	if err := api.registerMultiAgentMCPServerTools(registrar, nil); err != nil {
		t.Fatal(err)
	}
	_, err := registrar.tools["install_mcp_server"].exec(context.Background(), map[string]interface{}{"name": "Connector", "url": "https://provider.example/mcp"})
	if err == nil || !strings.Contains(err.Error(), "authenticated user") {
		t.Fatalf("anonymous install should fail before touching config: %v", err)
	}
}

func TestMCPInstallBridgeStartsOAuthInPrivateTokenDirectory(t *testing.T) {
	for _, reconnect := range []bool{false, true} {
		t.Run(fmt.Sprintf("reconnect=%v", reconnect), func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			withMCPConnectionsRoot(t)
			t.Setenv("MULTI_USER_MODE", "false")
			t.Setenv("PUBLIC_URL", "https://app.example")
			name := "TestOwnerConnector"
			cfg := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{
				name: {URL: "https://provider.example/mcp", OAuth: &oauth.OAuthConfig{
					AuthURL: "https://provider.example/authorize", TokenURL: "https://provider.example/token", ClientID: "test-client",
				}},
			}}
			configPath := filepath.Join(t.TempDir(), "mcp.json")
			if err := mcpclient.SaveConfig(configPath, cfg); err != nil {
				t.Fatal(err)
			}
			// The definition may already exist after a failed/other-account install.
			// That must not short-circuit this account's authorization.
			if err := mcpclient.SaveConfig(strings.Replace(configPath, ".json", "_user.json", 1), cfg); err != nil {
				t.Fatal(err)
			}
			api := &StreamingAPI{mcpConfigPath: configPath, mcpConfig: cfg, logger: loggerv2.NewNoop(), eventStore: events.NewEventStore(10)}
			api.eventStore.SetSessionOwner("owner-chat", "alice")
			registrar := &recordingRegistrar{}
			if err := api.registerMultiAgentMCPServerTools(registrar, nil); err != nil {
				t.Fatal(err)
			}
			if reconnect {
				ownerToken := getUserTokenFilePath(platformMCPTokenUserID, name)
				if err := os.MkdirAll(filepath.Dir(ownerToken), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(ownerToken, []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			result, err := registrar.tools["install_mcp_server"].exec(executor.WithSessionID(context.Background(), "owner-chat"), map[string]interface{}{"name": name, "reconnect": reconnect})
			if err != nil {
				t.Fatal(err)
			}
			authURL, parseErr := url.Parse(result[strings.LastIndex(result, " ")+1:])
			if parseErr != nil || authURL.Query().Get("state") == "" {
				t.Fatalf("expected OAuth authorization URL: %v", parseErr)
			}
			oauthFlowsMu.Lock()
			flow := oauthFlows[authURL.Query().Get("state")]
			oauthFlowsMu.Unlock()
			if flow == nil {
				t.Fatal("flow was not registered")
			}
			defer func() { flow.ErrChan <- fmt.Errorf("test canceled before authorization") }()
			dir, _ := placeMCPDir("alice")
			if got := flow.ServerConfig.OAuth.TokenFile; !strings.HasPrefix(got, dir+string(os.PathSeparator)) {
				t.Fatalf("OAuth would save under wrong identity: %q", got)
			}

		})
	}
}
