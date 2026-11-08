package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	// MCP connections come only from Vault (PLAT-856, ee0e8ef837): the connection is one live Vault grant.
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"servers": []map[string]any{{"id": "c-notion-1", "label": "c-notion-1", "provider": "notion", "tools": []map[string]any{{"name": "notion_c-notion-1__notion-search"}}}}})
	}))
	defer upstream.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", upstream.URL)
	notion := vaultServerName("c-notion-1")
	other := vaultServerName("c-other")
	setSelection := func(names []string) {
		raw, _ := json.Marshal(map[string]interface{}{"schema_version": 1, "id": "scope-test", "label": "scope-test", "capabilities": map[string]interface{}{"selected_servers": names, "chosen_vault_apps_only": true}})
		ws.mu.Lock()
		ws.files[workspacePath+"/workflow.json"] = string(raw)
		ws.mu.Unlock()
	}
	configPath := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{mcpConfigPath: configPath, logger: loggerv2.NewNoop(), eventStore: events.NewEventStore(10)}
	api.eventStore.SetSessionOwner("same-chat", "alice")
	api.workshopChatSessions.Store("same-chat", &scopeWorkshop{&workshop.WorkshopConfig{WorkspacePath: workspacePath}})
	ctx := personContext("alice")
	setSelection([]string{other})
	if _, err := api.resolveWorkshopMCPServer(ctx, "same-chat", "notion", "search"); err == nil {
		t.Fatal("Notion allowed before selection")
	}
	setSelection([]string{other, notion})
	got, err := api.resolveWorkshopMCPServer(ctx, "same-chat", "notion", "search")
	if err != nil || vaultSelectionName(got.Name) != notion {
		t.Fatalf("selected Notion refused: %v %+v", err, got)
	}
	setSelection([]string{other})
	if _, err := api.resolveWorkshopMCPServer(ctx, "same-chat", "notion", "search"); err == nil {
		t.Fatal("removed Notion remained in scope")
	}
	ws.mu.Lock()
	ws.files[workspacePath+"/workflow.json"] = "invalid"
	ws.mu.Unlock()
	if _, err := api.resolveWorkshopMCPServer(ctx, "same-chat", "notion", "search"); err == nil {
		t.Fatal("failed open on invalid manifest")
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
