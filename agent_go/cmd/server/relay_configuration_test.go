package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Drive the configuration HTTP handlers against the workspace API: legacy
// values survive flattening, reads preserve release bytes, and saves remove
// the draft binding instead of silently restoring group execution.
func TestRelayConfigurationHTTPMigratesLegacyValuesWithoutChangingRelease(t *testing.T) {
	f := newExternalRelayFixture(t)
	workspace := "Workflow/invoices"
	f.mock.mu.Lock()
	var workflow WorkflowManifest
	if err := json.Unmarshal([]byte(f.mock.files[manifestPath(workspace)]), &workflow); err != nil {
		t.Fatal(err)
	}
	workflow.Schedules[0].GroupNames = []string{"customer"}
	raw, _ := json.Marshal(workflow)
	f.mock.files[manifestPath(workspace)] = string(raw)
	legacy := `{"variables":[{"name":"INPUT","value":"{}"},{"name":"ENDPOINT","value":"default"}],"groups":[{"name":"customer","values":{"ENDPOINT":"https://customer.example"},"enabled":true}]}`
	f.mock.files[workspace+"/variables/variables.json"] = legacy
	f.mock.mu.Unlock()
	release, err := publishRelayRelease(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	releaseWorkspace := relayReleaseWorkspace(workspace, release.Version)
	get := httptest.NewRecorder()
	f.api.handleGetVariableGroups(get, httptest.NewRequest(http.MethodGet, "/api/workflow/variable-groups?workspace_path="+workspace, nil))
	if get.Code != 200 {
		t.Fatalf("read: %d %s", get.Code, get.Body.String())
	}
	var view VariableGroupsResponse
	if err := json.Unmarshal(get.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Manifest == nil || len(view.Manifest.Groups) != 0 || view.Manifest.Variables[1].Value != "https://customer.example" {
		t.Fatalf("configuration view: %+v", view)
	}
	f.mock.mu.Lock()
	unchanged := f.mock.files[workspace+"/variables/variables.json"] == legacy && f.mock.files[releaseWorkspace+"/variables/variables.json"] == legacy
	f.mock.mu.Unlock()
	if !unchanged {
		t.Fatal("configuration read rewrote saved bytes")
	}
	flattened, _ := json.Marshal(view.Manifest)
	save := httptest.NewRecorder()
	f.api.handleUpdateVariableGroups(save, httptest.NewRequest(http.MethodPut, "/api/workflow/variable-groups?workspace_path="+workspace, strings.NewReader(string(flattened))))
	if save.Code != 200 {
		t.Fatalf("save: %d %s", save.Code, save.Body.String())
	}
	current, _, err := ReadWorkflowManifest(t.Context(), workspace)
	if err != nil || len(current.Schedules[0].GroupNames) != 0 {
		t.Fatalf("draft still has group binding: %+v %v", current, err)
	}
	if err := verifyRelayRelease(t.Context(), release, releaseWorkspace); err != nil {
		t.Fatalf("configuration save mutated published release: %v", err)
	}
	f.api.scheduler = NewSchedulerService(f.api)
	reg := &recordingRegistrar{}
	policy := workflowChatPolicy{Mode: "builder", Origin: "interactive", Capabilities: map[string]bool{"plan_authoring": true}}
	if err := f.api.registerWebhookTools(reg, "owner", workspace, policy); err != nil {
		t.Fatal(err)
	}
	output, err := reg.tools["manage_workflow_webhook"].exec(t.Context(), map[string]interface{}{
		"action": "create", "kind": "function", "name": "Second function", "enabled": true, "route_selections": map[string]string{},
		"function": map[string]interface{}{"name": "second", "inputs": []map[string]interface{}{{"name": "INPUT", "type": "object", "required": true}}},
	})
	if err != nil {
		t.Fatalf("Builder could not create group-free trigger: %s %v", output, err)
	}
	current, _, err = ReadWorkflowManifest(t.Context(), workspace)
	if err != nil || len(current.Schedules) != 2 || len(current.Schedules[1].GroupNames) != 0 {
		t.Fatalf("Builder trigger retained groups: %+v %v", current, err)
	}

	f.mock.mu.Lock()
	f.mock.files[workspace+"/runs/relay-test/execution/answer/result.json"] = `{"ok":true}`
	f.mock.files[workspace+"/runs/iteration-2-hook/execution/answer/result.json"] = `{"ok":true}`
	f.mock.mu.Unlock()
	folders := httptest.NewRecorder()
	folderRequest := httptest.NewRequest(http.MethodGet, "/api/workflow/run-folders?workspace_path="+workspace, nil)
	folderRequest = folderRequest.WithContext(context.WithValue(folderRequest.Context(), UserContextKey, &UserClaims{UserID: "owner"}))
	f.api.handleGetRunFolders(folders, folderRequest)
	if folders.Code != 200 {
		t.Fatalf("run folders: %d %s", folders.Code, folders.Body.String())
	}
	var folderView RunFoldersResponse
	if err := json.Unmarshal(folders.Body.Bytes(), &folderView); err != nil {
		t.Fatal(err)
	}
	if len(folderView.Folders) != 2 {
		t.Fatalf("missing group-free folders: %s", folders.Body.String())
	}
	for _, folder := range folderView.Folders {
		if strings.Contains(folder.Name, "/") {
			t.Fatalf("Relay execution directory became a group: %s", folder.Name)
		}
	}
	for _, tc := range []struct{ workspace, body string }{{workspace, legacy}, {releaseWorkspace, string(flattened)}} {
		save := httptest.NewRecorder()
		f.api.handleUpdateVariableGroups(save, httptest.NewRequest(http.MethodPut, "/api/workflow/variable-groups?workspace_path="+tc.workspace, strings.NewReader(tc.body)))
		if save.Code != 400 {
			t.Fatalf("invalid configuration save accepted: %d %s", save.Code, save.Body.String())
		}
	}
}
