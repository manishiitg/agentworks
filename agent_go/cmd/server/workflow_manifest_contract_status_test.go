package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestGetWorkflowManifestIncludesManualContractUpgradeStatus(t *testing.T) {
	const workspacePath = "Workflow/manual-upgrade"
	manifestJSON, _ := json.Marshal(map[string]interface{}{
		"schema_version": 1,
		"id":             "wf_manual_upgrade",
		"version":        "1.0.48",
		"label":          "Manual upgrade",
		"capabilities":   map[string]interface{}{},
		"schedules":      []interface{}{},
	})
	workspace := &mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/workflow.json": string(manifestJSON),
	}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/workflows/manifest?workspace_path="+url.QueryEscape(workspacePath), nil)
	(&StreamingAPI{}).handleGetWorkflowManifest(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var response struct {
		ContractUpgrade struct {
			Required        bool                              `json:"required"`
			CurrentVersion  string                            `json:"current_version"`
			PlatformVersion string                            `json:"platform_version"`
			PendingCount    int                               `json:"pending_count"`
			NextLabel       string                            `json:"next_label"`
			Pending         []workflowContractUpgradeListItem `json:"pending"`
			Applied         []workflowContractUpgradeListItem `json:"applied"`
			HistoryBasis    string                            `json:"history_basis"`
		} `json:"contract_upgrade"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.ContractUpgrade.Required || response.ContractUpgrade.CurrentVersion != "1.0.48" || response.ContractUpgrade.PlatformVersion != WorkflowContractCurrentVersion || response.ContractUpgrade.PendingCount == 0 || response.ContractUpgrade.NextLabel == "" {
		t.Fatalf("unexpected contract upgrade status: %+v", response.ContractUpgrade)
	}
	if len(response.ContractUpgrade.Pending) == 0 || len(response.ContractUpgrade.Applied) == 0 || response.ContractUpgrade.HistoryBasis != "contract_version" {
		t.Fatalf("missing upgrade lists: %+v", response.ContractUpgrade)
	}
	if got := response.ContractUpgrade.Pending[0].Label; got != response.ContractUpgrade.NextLabel {
		t.Fatalf("first pending label = %q, next label = %q", got, response.ContractUpgrade.NextLabel)
	}
	if response.ContractUpgrade.Pending[0].Details == "" {
		t.Fatal("pending migration details are empty")
	}
}

func TestWorkflowContractUpgradeListsIncludesCodeLayoutMigration(t *testing.T) {
	manifest := &WorkflowManifest{Version: WorkflowContractCurrentVersion, CodeLayoutVersion: 0}
	pending, applied := workflowContractUpgradeLists(manifest)
	if len(pending) != 1 || pending[0].Label != "upgrade-nested-agent-code-layout" {
		t.Fatalf("pending = %+v", pending)
	}
	if len(applied) == 0 {
		t.Fatal("expected earlier contract rungs to be shown as applied")
	}
}
