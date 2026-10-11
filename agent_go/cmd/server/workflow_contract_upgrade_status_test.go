package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// Upgrade instructions are visible to the operator who starts the migration;
// schedules neither own nor execute them.
func TestContractUpgradeStatusShowsWhatIsOwedAndTheActualInstruction(t *testing.T) {
	const workspacePath = "Workflow/customer-login"
	manifestJSON, err := json.Marshal(map[string]interface{}{
		"schema_version": 1,
		"id":             "wf_customer",
		"version":        "1.0.20",
		"label":          "customer-qa-testing",
		"capabilities":   map[string]interface{}{},
		"schedules":      []interface{}{},
	})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
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

	for _, want := range []string{
		"Current: `1.0.20`",
		"Pending migrations (22)",
		"upgrade-current-artifact-contract",
		"upgrade-direct-html-reports",
		"upgrade-schedule-execution-model",
		"upgrade-dedicated-pulse-schedule",
		"upgrade-schedule-prompt-contract",
		"upgrade-schedule-finalizer-ownership",
		"upgrade-report-activity-section",
		"upgrade-report-activity-tab",
		"upgrade-pulse-lifecycle-reconciliation",
		"upgrade-pulse-actionable-backlog",
		"upgrade-orchestrator-step-type",
		"upgrade-explicit-schedule-pulse",
		"upgrade-nested-agent-artifacts",
		"upgrade-managed-db-scripts",
		"upgrade-step-description-layout",
		// The full instruction text, not a summary of it — an owner judging
		// whether a stalled migration is safe needs the actual words.
		"NOTHING IS DELETED IN THIS MIGRATION",
		"Run them manually from this workflow's Workshop chat",
		"schedules never perform contract migrations",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q", want)
		}
	}
	if strings.Contains(out, "upgrade-run-scoped-routes") || strings.Contains(out, "migrate_run_scoped_routes") || strings.Contains(out, "upgrade-eval-retirement") || strings.Contains(out, `set_workflow_contract_version(version="1.0.43")`) {
		t.Errorf("status output still exposes a retired migration:\n%s", out)
	}
}

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
	for _, want := range []string{"Manual code-layout migration required", "code/<step-id>/", "set_code_layout_version(code_layout_version=1)", "Do not stamp another contract version"} {
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
