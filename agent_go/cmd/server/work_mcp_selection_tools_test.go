package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
)

func TestWorkMCPSelectionToolPersistsConnectedServerForProject(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("MULTI_USER_MODE", "false")
	const workspacePath = "_users/user-1/Chats/Work/projects/agentworks"
	const manifestPath = workspacePath + "/workflow.json"
	workspace := &mockWorkspaceAPI{files: map[string]string{
		manifestPath: `{"schema_version":1,"id":"agentworks","capabilities":{"selected_servers":[],"selected_skills":["review"]}}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)

	configPath := filepath.Join(t.TempDir(), "mcp_servers.json")
	base := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{
		"Linear": {URL: "https://linear.example/mcp"},
	}}
	if err := mcpclient.SaveConfig(configPath, base); err != nil {
		t.Fatal(err)
	}
	if err := mcpclient.SaveConfig(strings.Replace(configPath, ".json", "_user.json", 1), &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{
		"Linear": {URL: "https://linear.example/mcp"},
	}}); err != nil {
		t.Fatal(err)
	}

	_, _ = addPlaceMCPServer("user-1", placeMCPServer{Name: "linear", Catalog: "Linear", URL: "https://linear.example/mcp", Transport: "http"})
	api := &StreamingAPI{logger: loggerv2.NewNoop(), mcpConfig: base, mcpConfigPath: configPath}
	registrar := &recordingRegistrar{}
	if err := api.registerWorkMCPSelectionTool(registrar, "user-1", workspacePath); err != nil {
		t.Fatal(err)
	}
	tool, ok := registrar.tools[updateProjectMCPServerSelectionTool]
	if !ok {
		t.Fatal("Work MCP selection tool was not registered")
	}
	out, err := tool.exec(context.Background(), map[string]interface{}{"action": "select", "server": "linear"})
	if err != nil || !strings.Contains(out, `"takes_effect":"next_user_message"`) {
		t.Fatalf("select output=%s err=%v", out, err)
	}

	selected, initialized, err := productSelectedServers(context.Background(), "work", workspacePath)
	if err != nil || !initialized || len(selected) != 1 || selected[0] != "linear" {
		t.Fatalf("selected=%v initialized=%v err=%v", selected, initialized, err)
	}
	workspace.mu.Lock()
	saved := workspace.files[manifestPath]
	workspace.mu.Unlock()
	var manifest map[string]interface{}
	if err := json.Unmarshal([]byte(saved), &manifest); err != nil {
		t.Fatal(err)
	}
	capabilities := manifest["capabilities"].(map[string]interface{})
	if _, ok := capabilities["selected_skills"]; !ok {
		t.Fatalf("selection update lost unrelated capabilities: %s", saved)
	}

	out, err = tool.exec(context.Background(), map[string]interface{}{"action": "deselect", "server": "Linear"})
	if err != nil || !strings.Contains(out, `"selected_servers":[]`) {
		t.Fatalf("deselect output=%s err=%v", out, err)
	}
}

func TestWorkMCPSelectionToolRejectsUnconnectedServer(t *testing.T) {
	const workspacePath = "_users/user-1/Chats/Work/projects/agentworks"
	const manifestPath = workspacePath + "/workflow.json"
	workspace := &mockWorkspaceAPI{files: map[string]string{
		manifestPath: `{"schema_version":1,"id":"agentworks","capabilities":{"selected_servers":[]}}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)

	configPath := filepath.Join(t.TempDir(), "mcp_servers.json")
	base := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{
		"Linear": {URL: "https://linear.example/mcp"},
	}}
	if err := mcpclient.SaveConfig(configPath, base); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{logger: loggerv2.NewNoop(), mcpConfig: base, mcpConfigPath: configPath}
	registrar := &recordingRegistrar{}
	if err := api.registerWorkMCPSelectionTool(registrar, "user-1", workspacePath); err != nil {
		t.Fatal(err)
	}
	_, err := registrar.tools[updateProjectMCPServerSelectionTool].exec(context.Background(), map[string]interface{}{"action": "select", "server": "Linear"})
	if err == nil {
		t.Fatalf("expected unconnected MCP rejection, got %v", err)
	}
	selected, _, readErr := productSelectedServers(context.Background(), "work", workspacePath)
	if readErr != nil || len(selected) != 0 {
		t.Fatalf("rejected selection changed manifest: selected=%v err=%v", selected, readErr)
	}
}

func TestWorkMCPSelectionToolRequiresOwnedActiveProject(t *testing.T) {
	api := &StreamingAPI{}
	for _, workspacePath := range []string{
		"Chats/Work/projects",
		"_users/other/Chats/Work/projects/agentworks",
	} {
		if err := api.registerWorkMCPSelectionTool(&recordingRegistrar{}, "user-1", workspacePath); err == nil {
			t.Fatalf("registered project MCP selection for %q", workspacePath)
		}
	}
}
