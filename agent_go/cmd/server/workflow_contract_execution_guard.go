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

// Individual step tests do not wait for an AI drift review. Interactive full
// runs offer review when due; scheduled full runs retain their pre-run policy.
type workflowReviewPreRunRegistrar struct {
	definitionRegistrar
	api           *StreamingAPI
	sessionID     string
	workspacePath string
	interactive   bool
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
	if name == "run_full_workflow" {
		if r.interactive {
			// Copy the schema so another surface's saved definition is unchanged.
			copySchema := make(map[string]interface{}, len(schema))
			for key, value := range schema {
				copySchema[key] = value
			}
			properties := map[string]interface{}{}
			if original, ok := schema["properties"].(map[string]interface{}); ok {
				for key, value := range original {
					properties[key] = value
				}
			}
			properties["review_plan"] = map[string]interface{}{
				"type": "boolean", "description": "The user's choice when Plan Drift is due: true reviews before running; false runs without an AI drift review. Omit to receive the review-or-run choice when needed. Do not infer a choice from an ordinary run request. Basic validation and permissions always apply.",
			}
			copySchema["properties"] = properties
			schema = copySchema
			description += " Individual step tests do not require Plan Drift. For this full run, ask the user whether to review first or run now when the tool offers that choice; pass their choice as review_plan."
		}
		original := execute
		execute = func(ctx context.Context, args map[string]interface{}) (string, error) {
			interactive := r.interactive
			if interactive && r.api != nil {
				// Retained tool callbacks can survive into a background turn of
				// the same chat. Recheck current provenance before honoring skip.
				active, _ := r.api.getActiveSession(r.sessionID)
				interactive = personAttendedWorkflowReview(r.sessionID, "workflow-builder", QueryRequest{}, active, workflowChatPolicy{Origin: "interactive"})
			}
			if interactive {
				if raw, supplied := args["review_plan"]; supplied {
					review, ok := raw.(bool)
					if !ok {
						return "", fmt.Errorf("review_plan must be true or false")
					}
					if !review {
						return original(ctx, args)
					}
				} else if message := r.api.manualWorkflowReviewChoice(r.workspacePath); message != "" {
					return message, nil
				}
			}
			proceed, message := r.api.manualRunWorkflowReview(ctx, r.sessionID, r.workspacePath, "", name)
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
