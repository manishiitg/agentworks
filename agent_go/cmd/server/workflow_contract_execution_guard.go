package server

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// workflowContractExecutionGuardRegistrar blocks manual workflow execution on
// an old platform contract. Unattended starts use directWebhookPreflight with
// the same contract requirement. Keeping this at tool execution
// time also covers restored chats and every UI/chat path that invokes these
// tools.
type workflowContractExecutionGuardRegistrar struct {
	definitionRegistrar
	workspacePath string
	scheduled     bool
	boundWorkflow *crewExecutableBinding
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
			if r.scheduled {
				binding, err := findCrewExecutable(r.workspacePath)
				if err != nil {
					log.Printf("workflow_binding_lookup_failed workspace=%q error=%v; keeping unmoved schedule behavior", r.workspacePath, err)
					return original(ctx, args)
				}
				if binding == nil {
					return original(ctx, args) // Unmoved server retains its current schedule behavior.
				}
			}
			if err := requireCurrentWorkflowContractForManualRun(ctx, r.workspacePath, r.boundWorkflow); err != nil {
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

func requireCurrentWorkflowContractForManualRun(ctx context.Context, workspacePath string, _ ...*crewExecutableBinding) error {
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
	if workflowContractNeedsRecreation(manifest) {
		return fmt.Errorf("%s", oldWorkflowContractMessage)
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
	nextStep := "Converting an existing workflow to a Python script is done by the platform operator for now."
	return fmt.Errorf(
		"workflow_contract_migration_required: this workflow is on v%s while the platform requires v%s; no step was started.%s %s",
		current,
		WorkflowContractCurrentVersion,
		next,
		nextStep,
	)
}
