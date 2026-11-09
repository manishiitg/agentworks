package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestWorkMCPSelectionToolPersistsConnectedServerForProject(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	workspacePath := registerTestCrew(t, "agentworks", "owner", false, "agentworks")
	manifestPath := workspacePath + "/workflow.json"
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	workspace := &mockWorkspaceAPI{files: map[string]string{
		manifestPath: `{"schema_version":1,"id":"agentworks","capabilities":{"selected_servers":[],"selected_skills":["review"]}}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	vault := newCrewConnectionVaultFixture(t)
	const id = "c-00000001"
	vault.connections[id] = map[string]any{"ID": id, "VaultID": "v-123456", "Provider": "example", "Status": "active"}
	api := &StreamingAPI{logger: loggerv2.NewNoop()}
	registrar := &recordingRegistrar{}
	if err := api.registerWorkMCPSelectionTool(registrar, "owner", workspacePath); err != nil {
		t.Fatal(err)
	}
	tool, ok := registrar.tools[updateProjectMCPServerSelectionTool]
	if !ok {
		t.Fatal("Crew MCP selection tool was not registered")
	}
	if _, err := tool.exec(ctx, map[string]interface{}{"action": "select", "server": vaultServerName(id)}); err != nil {
		t.Fatal(err)
	}
	selected, initialized, err := productSelectedServers(ctx, "work", workspacePath)
	if err != nil || !initialized || len(selected) != 1 || selected[0] != vaultServerName(id) {
		t.Fatalf("selected=%v initialized=%v err=%v", selected, initialized, err)
	}
	workspace.mu.Lock()
	saved := workspace.files[manifestPath]
	workspace.mu.Unlock()
	var manifest map[string]interface{}
	if err := json.Unmarshal([]byte(saved), &manifest); err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest["capabilities"].(map[string]interface{})["selected_skills"]; !ok {
		t.Fatal("selection update lost unrelated capabilities")
	}
	if _, err := tool.exec(ctx, map[string]interface{}{"action": "deselect", "server": vaultServerName(id)}); err != nil {
		t.Fatal(err)
	}
	if selected, _, err := productSelectedServers(ctx, "work", workspacePath); err != nil || len(selected) != 0 {
		t.Fatalf("deselect did not persist: selected=%v err=%v", selected, err)
	}
}

func TestWorkMCPSelectionToolRejectsUnconnectedServer(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	workspacePath := registerTestCrew(t, "agentworks", "owner", false, "agentworks")
	manifestPath := workspacePath + "/workflow.json"
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	workspace := &mockWorkspaceAPI{files: map[string]string{
		manifestPath: `{"schema_version":1,"id":"agentworks","capabilities":{"selected_servers":[]}}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	newCrewConnectionVaultFixture(t)
	api := &StreamingAPI{logger: loggerv2.NewNoop()}
	registrar := &recordingRegistrar{}
	if err := api.registerWorkMCPSelectionTool(registrar, "owner", workspacePath); err != nil {
		t.Fatal(err)
	}
	// A real, syntactically valid connection belongs to another Vault; the refusal must be access, not a malformed name.
	if _, err := registrar.tools[updateProjectMCPServerSelectionTool].exec(ctx, map[string]interface{}{"action": "select", "server": vaultServerName(crewForeignConnection)}); err == nil {
		t.Fatal("accepted another Vault's connection")
	}
	if selected, _, err := productSelectedServers(ctx, "work", workspacePath); err != nil || len(selected) != 0 {
		t.Fatalf("rejected selection changed manifest: selected=%v err=%v", selected, err)
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
