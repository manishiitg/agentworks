package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/spf13/viper"
)

// newPreValidationConcernTestOrchestrator wires a REAL workspace-docs HTTP
// server and WORKSPACE_DOCS_PATH at the same root to verify the retained
// attempt logs produced by SavePreValidationLog.
func newPreValidationConcernTestOrchestrator(t *testing.T) (*StepBasedWorkflowOrchestrator, string) {
	t.Helper()

	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewDefault(), nil, orchestrator.OrchestratorTypeWorkflow,
		"", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator: %v", err)
	}
	workflowRelPath := "Workflow/pre-validation-concern-test"
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}
	hcpo.selectedRunFolder = "iteration-0/default"
	hcpo.currentGroupName = "default"

	docsDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(docsDir, workflowRelPath, "db"), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	t.Setenv("WORKSPACE_DOCS_PATH", docsDir)
	gin.SetMode(gin.TestMode)
	viper.Set("docs-dir", docsDir)
	router := gin.New()
	router.Any("/api/documents/*filepath", workspacehandlers.HandleDocumentRequest)
	wsServer := httptest.NewServer(router)
	t.Cleanup(wsServer.Close)

	hcpo.WorkspaceClient = workspace.NewClient(wsServer.URL)
	hcpo.SetWorkspacePath(workflowRelPath)

	return hcpo, filepath.Join(docsDir, workflowRelPath)
}

func targetingAuditFailure(field, message string) *WorkspaceVerificationResult {
	return &WorkspaceVerificationResult{
		OverallPass: false,
		Summary: ValidationSummary{
			TotalChecks: 1, FailedChecks: 1,
			Errors: []ValidationError{{File: "targeting_audit.json", Path: field, Message: message}},
		},
	}
}

func TestSavePreValidationLogRetainsEveryScriptedAndAgentAttempt(t *testing.T) {
	hcpo, _ := newPreValidationConcernTestOrchestrator(t)
	ctx := context.Background()
	logPath := hcpo.GetWorkspacePath() + "/runs/" + hcpo.selectedRunFolder

	scriptedStep := "read-credentials"
	SavePreValidationLog(ctx, hcpo.BaseOrchestrator, logPath, scriptedStep, scriptedStep,
		targetingAuditFailure("$.credentials", "missing credentials"), nil,
		hcpo.GetWorkspacePath(), hcpo.selectedRunFolder, hcpo.currentGroupName,
		PreValidationAttempt{ExecutionMode: "scripted", ValidationPhase: "repair-check", ExecutionAttempt: 1, ValidationAttempt: 1})
	SavePreValidationLog(ctx, hcpo.BaseOrchestrator, logPath, scriptedStep, scriptedStep,
		&WorkspaceVerificationResult{OverallPass: true, Summary: ValidationSummary{TotalChecks: 1, PassedChecks: 1}}, nil,
		hcpo.GetWorkspacePath(), hcpo.selectedRunFolder, hcpo.currentGroupName,
		PreValidationAttempt{ExecutionMode: "scripted", ValidationPhase: "repair-check", ExecutionAttempt: 1, ValidationAttempt: 2})

	messageStep := "collect-input"
	SavePreValidationLog(ctx, hcpo.BaseOrchestrator, logPath, messageStep, messageStep,
		targetingAuditFailure("$.input", "missing input"), nil,
		hcpo.GetWorkspacePath(), hcpo.selectedRunFolder, hcpo.currentGroupName,
		PreValidationAttempt{ExecutionMode: "agent", ValidationPhase: "agent-verify", ExecutionAttempt: 1, ValidationAttempt: 1})
	SavePreValidationLog(ctx, hcpo.BaseOrchestrator, logPath, messageStep, messageStep,
		&WorkspaceVerificationResult{OverallPass: true, Summary: ValidationSummary{TotalChecks: 1, PassedChecks: 1}}, nil,
		hcpo.GetWorkspacePath(), hcpo.selectedRunFolder, hcpo.currentGroupName,
		PreValidationAttempt{ExecutionMode: "agent", ValidationPhase: "agent-verify", ExecutionAttempt: 1, ValidationAttempt: 2})

	read := func(path string) PreValidationLogEntry {
		t.Helper()
		raw, err := hcpo.ReadWorkspaceFile(ctx, path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var entry PreValidationLogEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		return entry
	}

	for _, tc := range []struct {
		step, phase, mode string
	}{
		{scriptedStep, "repair-check", "scripted"},
		{messageStep, "agent-verify", "agent"},
	} {
		base := fmt.Sprintf("%s/runs/%s/logs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder, tc.step)
		failed := read(fmt.Sprintf("%s/pre_validation_%s_execution_001_attempt_001.json", base, tc.phase))
		passed := read(fmt.Sprintf("%s/pre_validation_%s_execution_001_attempt_002.json", base, tc.phase))
		latest := read(base + "/pre_validation.json")
		if failed.OverallPass || failed.ValidationAttempt != 1 || failed.ExecutionMode != tc.mode || len(failed.Errors) == 0 {
			t.Fatalf("failed %s attempt was not retained accurately: %+v", tc.mode, failed)
		}
		if !passed.OverallPass || passed.ValidationAttempt != 2 || !latest.OverallPass || latest.ValidationAttempt != 2 {
			t.Fatalf("latest %s result should be the passing second attempt: passed=%+v latest=%+v", tc.mode, passed, latest)
		}
	}
}
