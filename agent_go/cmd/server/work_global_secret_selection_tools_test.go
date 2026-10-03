package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkGlobalSecretSelectionToolPersistsExistingGlobal(t *testing.T) {
	withVaultSecretGrant(t, "user-1", "GITHUB_TOKEN_READ_ONLY")
	const workspacePath = "_users/user-1/Chats/Work/projects/release"
	const manifestPath = workspacePath + "/workflow.json"
	workspace := &mockWorkspaceAPI{files: map[string]string{
		manifestPath: `{"schema_version":1,"id":"release","capabilities":{"selected_global_secret_names":[],"selected_skills":["review"]}}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)

	managedGlobalsMu.Lock()
	previousGlobals := globalSecrets
	previousManaged := managedGlobals
	globalSecrets = []globalSecretEntry{{Name: "GITHUB_TOKEN_READ_ONLY", Value: "never-return-this"}}
	managedGlobals = map[string]string{}
	managedGlobalsMu.Unlock()
	t.Cleanup(func() {
		managedGlobalsMu.Lock()
		globalSecrets = previousGlobals
		managedGlobals = previousManaged
		managedGlobalsMu.Unlock()
	})

	registrar := &recordingRegistrar{}
	api := &StreamingAPI{}
	if err := api.registerWorkGlobalSecretSelectionTool(registrar, "user-1", workspacePath); err != nil {
		t.Fatal(err)
	}
	tool, ok := registrar.tools[updateProjectGlobalSecretSelectionTool]
	if !ok {
		t.Fatal("Crew global secret selection tool was not registered")
	}
	out, err := tool.exec(context.Background(), map[string]interface{}{"action": "select", "name": "github_token_read_only"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"name":"GITHUB_TOKEN_READ_ONLY"`) || strings.Contains(out, "never-return-this") {
		t.Fatalf("selection output exposed a value or lost the canonical name: %s", out)
	}
	selected, err := productSelectedGlobalSecrets(context.Background(), "work", workspacePath)
	if err != nil || len(*selected) != 1 || (*selected)[0] != "GITHUB_TOKEN_READ_ONLY" {
		t.Fatalf("selected=%v err=%v", *selected, err)
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

	out, err = tool.exec(context.Background(), map[string]interface{}{"action": "deselect", "name": "GITHUB_TOKEN_READ_ONLY"})
	if err != nil || !strings.Contains(out, `"selected_global_secret_names":[]`) {
		t.Fatalf("deselect output=%s err=%v", out, err)
	}
}

func TestWorkGlobalSecretSelectionToolRejectsUnknownGlobal(t *testing.T) {
	const workspacePath = "_users/user-1/Chats/Work/projects/release"
	const manifestPath = workspacePath + "/workflow.json"
	workspace := &mockWorkspaceAPI{files: map[string]string{
		manifestPath: `{"schema_version":1,"id":"release","capabilities":{"selected_global_secret_names":[]}}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)

	managedGlobalsMu.Lock()
	previousGlobals := globalSecrets
	previousManaged := managedGlobals
	globalSecrets = nil
	managedGlobals = map[string]string{}
	managedGlobalsMu.Unlock()
	t.Cleanup(func() {
		managedGlobalsMu.Lock()
		globalSecrets = previousGlobals
		managedGlobals = previousManaged
		managedGlobalsMu.Unlock()
	})

	registrar := &recordingRegistrar{}
	if err := (&StreamingAPI{}).registerWorkGlobalSecretSelectionTool(registrar, "user-1", workspacePath); err != nil {
		t.Fatal(err)
	}
	_, err := registrar.tools[updateProjectGlobalSecretSelectionTool].exec(context.Background(), map[string]interface{}{"action": "select", "name": "MISSING"})
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("expected unknown global rejection, got %v", err)
	}
	selected, readErr := productSelectedGlobalSecrets(context.Background(), "work", workspacePath)
	if readErr != nil || len(*selected) != 0 {
		t.Fatalf("rejected selection changed manifest: selected=%v err=%v", *selected, readErr)
	}
}

func TestWorkGlobalSecretSelectionToolRequiresOwnedActiveProject(t *testing.T) {
	api := &StreamingAPI{}
	for _, workspacePath := range []string{
		"Chats/Work/projects",
		"_users/other/Chats/Work/projects/release",
	} {
		if err := api.registerWorkGlobalSecretSelectionTool(&recordingRegistrar{}, "user-1", workspacePath); err == nil {
			t.Fatalf("registered Crew global secret selection for %q", workspacePath)
		}
	}
}
