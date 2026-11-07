package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveContractGuardManifest(t *testing.T, version string) string {
	t.Helper()
	ensureProjectStateRoot(t)
	const workspacePath = "Workflow/contract-guard"
	data, err := json.Marshal(map[string]interface{}{
		"schema_version":      1,
		"version":             version,
		"code_layout_version": 1,
		"id":                  "wf-contract-guard",
		"label":               "Contract guard",
		"capabilities":        map[string]interface{}{},
		"schedules":           []interface{}{},
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace := &mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/workflow.json": string(data),
	}}
	server := httptest.NewServer(workspace)
	t.Cleanup(server.Close)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	return workspacePath
}

func TestRequireCurrentWorkflowContractForManualRunAllowsCurrent(t *testing.T) {
	workspacePath := serveContractGuardManifest(t, WorkflowContractCurrentVersion)
	if err := requireCurrentWorkflowContractForManualRun(context.Background(), workspacePath); err != nil {
		t.Fatalf("current contract blocked: %v", err)
	}
}

func TestManualRunRefusesUnsupportedOldContracts(t *testing.T) {
	for _, version := range []string{
		workflowContractExplicitSchedulePulseVersion,
		workflowContractRunScopedRoutesVersion,
		workflowContractEvalRetirementVersion,
	} {
		t.Run(version, func(t *testing.T) {
			workspacePath := serveContractGuardManifest(t, version)
			err := requireCurrentWorkflowContractForManualRun(context.Background(), workspacePath)
			if err == nil {
				t.Fatalf("older marker %s did not explain manual recreation: %v", version, err)
			}
		})
	}
}

func TestRequireCurrentWorkflowContractForManualRunRejectsUnknownVersion(t *testing.T) {
	workspacePath := serveContractGuardManifest(t, "99.0.0")
	err := requireCurrentWorkflowContractForManualRun(context.Background(), workspacePath)
	if err == nil || !strings.Contains(err.Error(), "workflow_contract_migration_required") {
		t.Fatalf("unknown contract must block safely, got %v", err)
	}
}

func TestWorkflowContractExecutionGuardWrapsBothManualRunTools(t *testing.T) {
	workspacePath := serveContractGuardManifest(t, workflowContractRouteSummariesVersion)
	draft := &productSurfaceDraft{}
	guard := workflowContractExecutionGuardRegistrar{
		definitionRegistrar: draft,
		workspacePath:       workspacePath,
	}
	called := 0
	for _, name := range []string{"execute_step", "run_full_workflow"} {
		if err := guard.RegisterCustomTool(name, "test", map[string]interface{}{}, func(context.Context, map[string]interface{}) (string, error) {
			called++
			return "started", nil
		}, "workflow"); err != nil {
			t.Fatal(err)
		}
		_, err := draft.tools[name].exec(context.Background(), nil)
		if err == nil {
			t.Fatalf("%s did not return migration prompt: %v", name, err)
		}
	}
	if called != 0 {
		t.Fatalf("guarded executors ran %d time(s)", called)
	}
}
