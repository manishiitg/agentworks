package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRetiredWorkflowUpgradeGivesBuilderNextStep(t *testing.T) {
	for _, version := range []string{"", "1.0.9", "1.0.43"} {
		manifest := &WorkflowManifest{SchemaVersion: 1, ID: "old-workflow", Version: version, CodeLayoutVersion: 1}
		data, _ := json.Marshal(manifest)
		const path = "Workflow/old-workflow"
		server := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{path + "/workflow.json": string(data)}})
		t.Setenv("WORKSPACE_API_URL", server.URL)
		message, err := describeWorkflowContractUpgrades(context.Background(), path)
		if err != nil || message != oldWorkflowContractMessage {
			t.Fatalf("version %q: %q %v", version, message, err)
		}
		if plan := workflowVersionUpgradePlan(manifest); len(plan) != 0 {
			t.Fatalf("retired upgrade instructions: %+v", plan)
		}
		if err := requireCurrentWorkflowContractForManualRun(context.Background(), path); err == nil || !strings.Contains(err.Error(), oldWorkflowContractMessage) {
			t.Fatalf("missing Builder instruction: %v", err)
		}
		if err := directWebhookPreflight(manifest); err == nil || !strings.Contains(err.Error(), oldWorkflowContractMessage) {
			t.Fatalf("missing webhook instruction: %v", err)
		}
		server.Close()
	}
	plan := workflowVersionUpgradePlan(&WorkflowManifest{Version: "1.0.50"})
	if len(plan) != 1 || plan[0].to != "1.0.51" || !strings.Contains(plan[0].query, "keeps the old learning files intact") {
		t.Fatalf("skills upgrade changed: %+v", plan)
	}
}
