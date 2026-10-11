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
// server (for SavePreValidationLog's file write) AND WORKSPACE_DOCS_PATH (for
// the run_concerns sqlite store) at the SAME root, so a test can
// verify both real artifacts SavePreValidationLog produces.
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

// Prevalidation is durable run evidence, not a Go-created Pulse observation.
// Technical Review reads the retained log and decides whether it is a real
// workflow issue; this avoids turning every malformed retry into repair debt.
func TestSavePreValidationLogKeepsFailureOutOfPulseRegister(t *testing.T) {
	hcpo, _ := newPreValidationConcernTestOrchestrator(t)
	ctx := context.Background()
	stepID := "execute-targeting-audit"
	logPath := hcpo.GetWorkspacePath() + "/runs/" + hcpo.selectedRunFolder

	SavePreValidationLog(ctx, hcpo.BaseOrchestrator, logPath, stepID, stepID,
		targetingAuditFailure("$.our_follower_count", "unknown key our_follower_count"), nil,
		hcpo.GetWorkspacePath(), hcpo.selectedRunFolder, hcpo.currentGroupName)

	concerns := activeRunConcerns(t, hcpo.GetWorkspacePath())
	if len(concerns) != 0 {
		t.Fatalf("prevalidation evidence must not create a raw Pulse concern: %+v", concerns)
	}
}

// TestSavePreValidationLogConcernRecursAcrossRuns proves the concern survives
// past the run that raised it and its own log-file overwrite -- the same
// field failing on a LATER run (a fresh pre_validation.json, a fresh
// pre-run-folder) still accumulates onto the SAME db row instead of starting
// over, which is exactly the cross-run chronic-defect signal a per-run JSON
// file structurally cannot provide.
func TestSavePreValidationLogRepeatedFailuresStayOutOfPulseRegister(t *testing.T) {
	hcpo, _ := newPreValidationConcernTestOrchestrator(t)
	ctx := context.Background()
	stepID := "execute-targeting-audit"

	for _, run := range []string{"iteration-0/default", "iteration-0/default-2"} {
		hcpo.selectedRunFolder = run
		logPath := hcpo.GetWorkspacePath() + "/runs/" + run
		SavePreValidationLog(ctx, hcpo.BaseOrchestrator, logPath, stepID, stepID,
			targetingAuditFailure("$.validated_targeting.follower_max", "Expected number, got <nil>"), nil,
			hcpo.GetWorkspacePath(), hcpo.selectedRunFolder, hcpo.currentGroupName)
	}

	concerns := activeRunConcerns(t, hcpo.GetWorkspacePath())
	if len(concerns) != 0 {
		t.Fatalf("repeated prevalidation evidence must remain in retained logs, not raw Pulse concerns: %+v", concerns)
	}
}

func TestSavePreValidationLogDoesNotCreateCollapsedPulseConcern(t *testing.T) {
	hcpo, _ := newPreValidationConcernTestOrchestrator(t)
	ctx := context.Background()
	stepID := "execute-find-opportunities"
	logPath := hcpo.GetWorkspacePath() + "/runs/" + hcpo.selectedRunFolder

	failure := &WorkspaceVerificationResult{
		OverallPass: false,
		Summary: ValidationSummary{
			TotalChecks:  3,
			FailedChecks: 3,
			Errors: []ValidationError{
				{File: "opportunities.json", Path: "$.action_targets", Message: "must exist"},
				{File: "opportunities.json", Path: "$.coverage_report", Message: "must be an object"},
				{File: "opportunities.json", Path: "$.targets", Message: "must be an array"},
			},
		},
	}
	SavePreValidationLog(ctx, hcpo.BaseOrchestrator, logPath, stepID, stepID,
		failure, nil, hcpo.GetWorkspacePath(), hcpo.selectedRunFolder, hcpo.currentGroupName)

	concerns := activeRunConcerns(t, hcpo.GetWorkspacePath())
	if len(concerns) != 0 {
		t.Fatalf("failed checks must remain retained evidence until Technical Review classifies them: %+v", concerns)
	}

	// A repair retry in the same run updates the one bug's latest evidence but
	// does not manufacture another recurrence.
	SavePreValidationLog(ctx, hcpo.BaseOrchestrator, logPath, stepID, stepID,
		targetingAuditFailure("$.new_field", "still missing"), nil,
		hcpo.GetWorkspacePath(), hcpo.selectedRunFolder, hcpo.currentGroupName)
	concerns = activeRunConcerns(t, hcpo.GetWorkspacePath())
	if len(concerns) != 0 {
		t.Fatalf("same-run retry must not create Pulse register noise: %+v", concerns)
	}
}

// TestSavePreValidationLogPassNoConcern proves the common case (a step passes
// on its first attempt) files nothing -- no concern noise for healthy steps.
func TestSavePreValidationLogPassNoConcern(t *testing.T) {
	hcpo, _ := newPreValidationConcernTestOrchestrator(t)
	ctx := context.Background()
	stepID := "clean-step"
	logPath := hcpo.GetWorkspacePath() + "/runs/" + hcpo.selectedRunFolder

	SavePreValidationLog(ctx, hcpo.BaseOrchestrator, logPath, stepID, stepID,
		&WorkspaceVerificationResult{OverallPass: true, Summary: ValidationSummary{TotalChecks: 1, PassedChecks: 1}}, nil,
		hcpo.GetWorkspacePath(), hcpo.selectedRunFolder, hcpo.currentGroupName)

	concerns := activeRunConcerns(t, hcpo.GetWorkspacePath())
	if len(concerns) != 0 {
		t.Fatalf("expected no concerns for a passing result, got %+v", concerns)
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
