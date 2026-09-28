package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkSkillSelectionToolPersistsInstalledSkill(t *testing.T) {
	const workspacePath = "_users/user-1/Chats/Work/projects/release"
	const manifestPath = workspacePath + "/workflow.json"
	workspace := &mockWorkspaceAPI{files: map[string]string{
		manifestPath:                     `{"schema_version":1,"id":"release","capabilities":{"selected_skills":[],"selected_servers":["Linear"]}}`,
		"skills/release-review/SKILL.md": "---\nname: release-review\ndescription: Review releases\n---\n\n# Release review\n",
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)

	registrar := &recordingRegistrar{}
	if err := (&StreamingAPI{}).registerWorkSkillSelectionTool(registrar, "user-1", workspacePath); err != nil {
		t.Fatal(err)
	}
	tool, ok := registrar.tools[updateProjectSkillSelectionTool]
	if !ok {
		t.Fatal("Crew skill selection tool was not registered")
	}
	out, err := tool.exec(context.Background(), map[string]interface{}{"action": "select", "skill": "release-review"})
	if err != nil || !strings.Contains(out, `"selected_skills":["release-review"]`) {
		t.Fatalf("select output=%s err=%v", out, err)
	}
	selected, err := productSelectedSkills(context.Background(), "work", workspacePath)
	if err != nil || len(selected) != 1 || selected[0] != "release-review" {
		t.Fatalf("selected=%v err=%v", selected, err)
	}

	workspace.mu.Lock()
	saved := workspace.files[manifestPath]
	workspace.mu.Unlock()
	var manifest map[string]interface{}
	if err := json.Unmarshal([]byte(saved), &manifest); err != nil {
		t.Fatal(err)
	}
	capabilities := manifest["capabilities"].(map[string]interface{})
	if _, ok := capabilities["selected_servers"]; !ok {
		t.Fatalf("skill selection lost unrelated capabilities: %s", saved)
	}

	out, err = tool.exec(context.Background(), map[string]interface{}{"action": "deselect", "skill": "RELEASE-REVIEW"})
	if err != nil || !strings.Contains(out, `"selected_skills":[]`) {
		t.Fatalf("deselect output=%s err=%v", out, err)
	}
}

func TestWorkSkillSelectionToolAcceptsProjectLocalSkill(t *testing.T) {
	const workspacePath = "_users/user-1/Chats/Work/projects/finance"
	workspace := &mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/workflow.json":                 `{"schema_version":1,"id":"finance","capabilities":{"selected_skills":[]}}`,
		workspacePath + "/skills/finance-analyst/SKILL.md": "---\nname: finance-analyst\ndescription: Analyze finance records\n---\n\n# Finance analyst\n",
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)

	registrar := &recordingRegistrar{}
	if err := (&StreamingAPI{}).registerWorkSkillSelectionTool(registrar, "user-1", workspacePath); err != nil {
		t.Fatal(err)
	}
	out, err := registrar.tools[updateProjectSkillSelectionTool].exec(context.Background(), map[string]interface{}{"action": "select", "skill": "finance-analyst"})
	if err != nil || !strings.Contains(out, `"selected_skills":["finance-analyst"]`) {
		t.Fatalf("select output=%s err=%v", out, err)
	}
}

func TestWorkSkillSelectionToolRejectsMissingSkill(t *testing.T) {
	const workspacePath = "_users/user-1/Chats/Work/projects/release"
	const manifestPath = workspacePath + "/workflow.json"
	workspace := &mockWorkspaceAPI{files: map[string]string{
		manifestPath: `{"schema_version":1,"id":"release","capabilities":{"selected_skills":[]}}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)

	registrar := &recordingRegistrar{}
	if err := (&StreamingAPI{}).registerWorkSkillSelectionTool(registrar, "user-1", workspacePath); err != nil {
		t.Fatal(err)
	}
	_, err := registrar.tools[updateProjectSkillSelectionTool].exec(context.Background(), map[string]interface{}{"action": "select", "skill": "missing"})
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("expected missing skill rejection, got %v", err)
	}
	selected, readErr := productSelectedSkills(context.Background(), "work", workspacePath)
	if readErr != nil || len(selected) != 0 {
		t.Fatalf("rejected selection changed manifest: selected=%v err=%v", selected, readErr)
	}
}

func TestWorkSkillSelectionToolRequiresOwnedActiveProject(t *testing.T) {
	api := &StreamingAPI{}
	for _, workspacePath := range []string{
		"Chats/Work/projects",
		"_users/other/Chats/Work/projects/release",
	} {
		if err := api.registerWorkSkillSelectionTool(&recordingRegistrar{}, "user-1", workspacePath); err == nil {
			t.Fatalf("registered Crew skill selection for %q", workspacePath)
		}
	}
}

func TestWorkSkillSelectionToolAcceptsBuiltinSkill(t *testing.T) {
	const workspacePath = "_users/user-1/Chats/Work/projects/release"
	workspace := &mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/workflow.json": `{"schema_version":1,"id":"release","capabilities":{"selected_skills":[]}}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)

	registrar := &recordingRegistrar{}
	if err := (&StreamingAPI{}).registerWorkSkillSelectionTool(registrar, "user-1", workspacePath); err != nil {
		t.Fatal(err)
	}
	out, err := registrar.tools[updateProjectSkillSelectionTool].exec(context.Background(), map[string]interface{}{"action": "select", "skill": "agent-browser"})
	if err != nil || !strings.Contains(out, `"selected_skills":["agent-browser"]`) {
		t.Fatalf("built-in skill select output=%s err=%v", out, err)
	}
}
