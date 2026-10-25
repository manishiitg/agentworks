package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContractUpgradeStatusIsQuietWhenNothingIsOwed(t *testing.T) {
	const workspacePath = "Workflow/current"
	manifestJSON, _ := json.Marshal(map[string]interface{}{
		"schema_version":      1,
		"id":                  "wf_current",
		"version":             WorkflowContractCurrentVersion,
		"code_layout_version": 1,
		"label":               "current",
		"capabilities":        map[string]interface{}{},
		"schedules":           []interface{}{},
	})
	workspace := &mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/workflow.json": string(manifestJSON),
	}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	out, err := describeWorkflowContractUpgrades(context.Background(), workspacePath)
	if err != nil {
		t.Fatalf("describeWorkflowContractUpgrades: %v", err)
	}
	if !strings.Contains(out, "No pending migrations") {
		t.Errorf("a current workflow should report nothing owed:\n%s", out)
	}
	if strings.Contains(out, "WORKFLOW CONTRACT UPGRADE") {
		t.Errorf("a current workflow should not be shown migration instructions:\n%s", out)
	}
}

func TestContractUpgradeStatusExplainsCurrentVersionWithLegacyCodeLayout(t *testing.T) {
	const workspacePath = "Workflow/current-legacy-code"
	manifestJSON, _ := json.Marshal(map[string]interface{}{
		"schema_version": 1,
		"id":             "wf_current_legacy_code",
		"version":        WorkflowContractCurrentVersion,
		"label":          "current legacy code",
		"capabilities":   map[string]interface{}{},
		"schedules":      []interface{}{},
	})
	workspace := &mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/workflow.json": string(manifestJSON),
	}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	out, err := describeWorkflowContractUpgrades(context.Background(), workspacePath)
	if err != nil {
		t.Fatalf("describeWorkflowContractUpgrades: %v", err)
	}
	for _, want := range []string{"Manual code-layout migration required", "code/<step-id>/", "ask the server administrator", "current version does not prove"} {
		if !strings.Contains(out, want) {
			t.Errorf("code-layout status missing %q:\n%s", want, out)
		}
	}
}

// A version this server does not know has no upgrade path at all. Interactive
// execution and direct webhooks fail closed, while schedules keep the saved
// contract. That distinction is said plainly rather than rendered as an empty
// list.
func TestContractUpgradeStatusExplainsAnUnknownVersion(t *testing.T) {
	const workspacePath = "Workflow/newer"
	manifestJSON, _ := json.Marshal(map[string]interface{}{
		"schema_version": 1,
		"id":             "wf_newer",
		"version":        "9.9.9",
		"label":          "newer",
		"capabilities":   map[string]interface{}{},
		"schedules":      []interface{}{},
	})
	workspace := &mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/workflow.json": string(manifestJSON),
	}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	out, err := describeWorkflowContractUpgrades(context.Background(), workspacePath)
	if err != nil {
		t.Fatalf("describeWorkflowContractUpgrades: %v", err)
	}
	for _, want := range []string{"not one this server knows", "no upgrade path", "direct webhooks will refuse to start", "schedules continue using the saved contract"} {
		if !strings.Contains(out, want) {
			t.Errorf("unknown-version status missing %q:\n%s", want, out)
		}
	}
}
