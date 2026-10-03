package server

import (
	"context"
	"encoding/json"
	oauth2 "golang.org/x/oauth2"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	events "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	workshop "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
)

type scopeWorkshop struct{ config *workshop.WorkshopConfig }

func (s *scopeWorkshop) GetConfig() *workshop.WorkshopConfig { return s.config }

func TestWorkshopMCPScopeReadsUpdatedSelectionOnEveryCall(t *testing.T) {
	const workspacePath = "Workflow/scope-test"
	ws := &mockWorkspaceAPI{files: map[string]string{}}
	server := httptest.NewServer(ws)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	setSelection := func(names []string) {
		raw, _ := json.Marshal(map[string]interface{}{"schema_version": 1, "id": "scope-test", "label": "scope-test", "capabilities": map[string]interface{}{"selected_servers": names}})
		ws.mu.Lock()
		ws.files[workspacePath+"/workflow.json"] = string(raw)
		ws.mu.Unlock()
	}
	configPath := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{"Jam":{"url":"https://jam.example/mcp"},"Notion":{"url":"https://notion.example/mcp","oauth":{"auth_url":"https://notion.example/auth","token_url":"https://notion.example/token"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{mcpConfigPath: configPath, logger: loggerv2.NewNoop(), eventStore: events.NewEventStore(10)}
	api.eventStore.SetSessionOwner("same-chat", "alice")
	api.workshopChatSessions.Store("same-chat", &scopeWorkshop{&workshop.WorkshopConfig{WorkspacePath: workspacePath}})
	_, _ = addPlaceMCPServer("alice", placeMCPServer{Name: "notion", Catalog: "Notion", URL: "https://notion.example/mcp", Transport: "http", OAuth: &oauth.OAuthConfig{AuthURL: "https://notion.example/auth", TokenURL: "https://notion.example/token"}})
	dirForLogin, _ := placeMCPDir("alice")
	if err := oauth.NewTokenStore(placeMCPTokenFile(dirForLogin, "alice", "notion")).Save(&oauth2.Token{AccessToken: "alice-test", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	setSelection([]string{"Jam"})
	if _, err := api.resolveWorkshopMCPServer(context.Background(), "same-chat", "Notion", "search"); err == nil {
		t.Fatal("Notion allowed before selection")
	}
	setSelection([]string{"Jam", "Notion"})
	got, err := api.resolveWorkshopMCPServer(context.Background(), "same-chat", "Notion", "search")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := placeMCPDir("alice")
	if got.Name != placeMCPInternalName("alice", "notion") || !strings.HasPrefix(got.Config.OAuth.TokenFile, dir+string(os.PathSeparator)) || got.ConnectionSessionID == platformMCPConnectionSessionID {
		t.Fatalf("wrong identity/config: %+v", got)
	}
	setSelection([]string{"Jam"})
	if _, err := api.resolveWorkshopMCPServer(context.Background(), "same-chat", "Notion", "search"); err == nil {
		t.Fatal("removed Notion remained in scope")
	}
	ws.mu.Lock()
	ws.files[workspacePath+"/workflow.json"] = "invalid"
	ws.mu.Unlock()
	if _, err := api.resolveWorkshopMCPServer(context.Background(), "same-chat", "Jam", "search"); err == nil {
		t.Fatal("failed open on invalid manifest")
	}
}

func TestSelectedMCPScopeAliasesToolRestrictionsAndPrivateIdentity(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("CAPLAYER_SERVICE_URL", "")
	cfg := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{"test-provider": {URL: "https://example.test/mcp"}, "Jam": {URL: "https://jam.example/mcp"}}}
	for _, person := range []string{"alice", "bob"} {
		_, err := addPlaceMCPServer(person, placeMCPServer{Name: "test_provider", Catalog: "test-provider", URL: "https://example.test/mcp", Transport: "http"})
		if err != nil {
			t.Fatal(err)
		}
	}
	api := &StreamingAPI{}
	a, err := api.resolveScopedGovernedMCP(context.Background(), cfg, []string{"test-provider"}, []string{"test-provider:search"}, "alice", "test_provider", "search")
	if err != nil {
		t.Fatal(err)
	}
	b, err := api.resolveScopedGovernedMCP(context.Background(), cfg, []string{"test-provider"}, nil, "bob", "test-provider", "search")
	if err != nil || a.ConnectionSessionID == b.ConnectionSessionID {
		t.Fatal("private connection pool was shared")
	}
	for _, tc := range []struct {
		server, tool    string
		selected, tools []string
	}{{"Jam", "search", []string{"test-provider"}, nil}, {"test-provider", "fetch", []string{"test-provider"}, []string{"test-provider:search"}}, {"test-provider", "search", nil, nil}} {
		if _, err := api.resolveScopedGovernedMCP(context.Background(), cfg, tc.selected, tc.tools, "alice", tc.server, tc.tool); err == nil {
			t.Fatal("selection failed open")
		}
	}
}
func TestSelectedMCPScopeDoesNotReuseLegacySharedCredentials(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("CAPLAYER_SERVICE_URL", "")
	cfg := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{"Linear": {URL: "https://mcp.linear.app/mcp", OAuth: &oauth.OAuthConfig{TokenFile: "/legacy/tokens/admin/Linear.json"}}}}
	api := &StreamingAPI{}
	for _, person := range []string{"alice", "bob"} {
		if _, err := api.resolveScopedGovernedMCP(context.Background(), cfg, []string{"Linear"}, nil, person, "Linear", "list_issues"); err == nil {
			t.Fatal("legacy platform credentials bypassed Vault")
		}
	}
}
