package server

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// workflowContractExecutionGuardRegistrar blocks manual workflow execution on
// an old platform contract. Scheduled runs deliberately bypass this interactive
// boundary; direct webhooks have directWebhookPreflight. Keeping this at tool execution
// time also covers restored chats and every UI/chat path that invokes these
// tools.
type workflowContractExecutionGuardRegistrar struct {
	definitionRegistrar
	workspacePath string
}

func (r workflowContractExecutionGuardRegistrar) RegisterCustomTool(
	name, description string,
	schema map[string]interface{},
	execute func(context.Context, map[string]interface{}) (string, error),
	group string,
) error {
	return r.RegisterCustomToolWithTimeout(name, description, schema, execute, 0, group)
}

func (r workflowContractExecutionGuardRegistrar) RegisterCustomToolWithTimeout(
	name, description string,
	schema map[string]interface{},
	execute func(context.Context, map[string]interface{}) (string, error),
	timeout time.Duration,
	group string,
) error {
	if name == "execute_step" || name == "run_full_workflow" {
		original := execute
		execute = func(ctx context.Context, args map[string]interface{}) (string, error) {
			if err := requireCurrentWorkflowContractForManualRun(ctx, r.workspacePath); err != nil {
				return "", err
			}
			stepID := ""
			if name == "execute_step" {
				stepID, _ = args["step_id"].(string)
			}
			notice, err := workflowGraphPreflight(r.workspacePath, stepID)
			if err != nil {
				return "", err
			}
			out, runErr := original(ctx, args)
			if notice != "" && runErr == nil {
				out = notice + "\n\n" + out
			}
			return out, runErr
		}
	}
	return r.definitionRegistrar.RegisterCustomToolWithTimeout(name, description, schema, execute, timeout, group)
}

// workflowReviewPreRunRegistrar runs the Workflow Review pre-run check
// (PLAT-697 phase 0) in front of execute_step and run_full_workflow in every
// workflow chat: Builder, Run, and Pulse turns. Clean plans start at once; a
// changed plan starts the review as a background job of the chat and the run
// tool returns without starting (workflow_review_prerun.go).
type workflowReviewPreRunRegistrar struct {
	definitionRegistrar
	api           *StreamingAPI
	sessionID     string
	workspacePath string
}

func (r workflowReviewPreRunRegistrar) RegisterCustomTool(
	name, description string,
	schema map[string]interface{},
	execute func(context.Context, map[string]interface{}) (string, error),
	group string,
) error {
	return r.RegisterCustomToolWithTimeout(name, description, schema, execute, 0, group)
}

func (r workflowReviewPreRunRegistrar) RegisterCustomToolWithTimeout(
	name, description string,
	schema map[string]interface{},
	execute func(context.Context, map[string]interface{}) (string, error),
	timeout time.Duration,
	group string,
) error {
	if name == "execute_step" || name == "run_full_workflow" {
		original := execute
		execute = func(ctx context.Context, args map[string]interface{}) (string, error) {
			stepID := ""
			if name == "execute_step" {
				stepID, _ = args["step_id"].(string)
			}
			proceed, message := r.api.manualRunWorkflowReview(ctx, r.sessionID, r.workspacePath, stepID, name)
			if !proceed {
				if strings.HasPrefix(message, "workflow_review_blocked:") {
					return "", fmt.Errorf("%s", message)
				}
				return message, nil
			}
			return original(ctx, args)
		}
	}
	return r.definitionRegistrar.RegisterCustomToolWithTimeout(name, description, schema, execute, timeout, group)
}

func requireCurrentWorkflowContractForManualRun(ctx context.Context, workspacePath string) error {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return fmt.Errorf("workflow_contract_check_failed: no workflow workspace is attached; no execution was started")
	}
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil {
		return fmt.Errorf("workflow_contract_check_failed: read workflow.json: %w", err)
	}
	if !found {
		return fmt.Errorf("workflow_contract_check_failed: workflow.json was not found at %s; no execution was started", workspacePath)
	}
	current := workflowContractVersionForUpgrade(manifest)
	if manifestContractIsExecutionCompatible(manifest) && manifest.CodeLayoutVersion == 1 {
		return nil
	}
	if manifestContractIsExecutionCompatible(manifest) {
		return fmt.Errorf("workflow_contract_migration_required: workflow contract v%s requires code_layout_version=1; no step was started. Ask the user: \"This workflow's scripted code must be migrated to code/<step-id>/ before it can run. Shall I migrate it now?\"", current)
	}

	pending := workflowVersionUpgradePlan(manifest)
	next := ""
	if len(pending) > 0 {
		next = fmt.Sprintf(" The next required migration is %s to v%s.", pending[0].label, pending[0].to)
	}
	return fmt.Errorf(
		"workflow_contract_migration_required: this workflow is on v%s while the platform requires v%s; no step was started.%s Ask the user: \"This workflow needs a platform migration before it can run. Shall I migrate it now?\" Only after approval, use Workshop get_contract_upgrades, complete and verify each migration in order, then retry the requested run",
		current,
		WorkflowContractCurrentVersion,
		next,
	)
}
