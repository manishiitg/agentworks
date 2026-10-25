package step_based_workflow

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/contractupgrade"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/scriptdb"
)

// NestedAgentArtifactsContractVersion is shared with the server's workflow
// contract ladder because the stamp executor must enforce this migration's
// source-layout prerequisite, not merely describe it in a prompt.
const NestedAgentArtifactsContractVersion = "1.0.44"

// ManagedDBScriptsContractVersion is the contract where scripted steps reach the
// workflow database through the built-in agentworks_db helper (the managed
// query/mutate tools) instead of opening db.sqlite themselves (PLAT-428).
const ManagedDBScriptsContractVersion = "1.0.45"

// StepDescriptionLayoutContractVersion is the contract where every plan step's
// description uses the standard section layout (## Goal, ## Inputs, ## Output,
// ## Rules, ## Done when, ## Guides; see the step-description guidance).
// validateStepDescriptionLayoutStamp gates the stamp (step_description_layout.go).
const StepDescriptionLayoutContractVersion = "1.0.46"

// validateManagedDBScriptsStamp refuses the 1.0.45 stamp while any script of the
// workflow (workflowDir is its absolute folder) still opens the database itself or
// carries a schema statement. Like the 1.0.44 layout check it lives in the stamp
// executor, not in the prompt: the stamp arrives by tool call or by shell, and
// only the executor sees both.
func validateManagedDBScriptsStamp(version, workflowDir string) error {
	if version != ManagedDBScriptsContractVersion {
		return nil
	}
	findings, scanned, err := scriptdb.ScanDir(filepath.Join(workflowDir, "code"))
	if err != nil {
		return fmt.Errorf("could not scan the scripts under code/: %w", err)
	}
	if len(findings) == 0 {
		return nil
	}
	return fmt.Errorf("%d of %d scripts still open the database themselves or carry schema statements:\n%s"+
		"Convert each to the agentworks_db helper (schema statements become db/migrations/ files applied with apply_workflow_db_migration), then run scan_workflow_script_db_usage until it is clean before stamping %s",
		len(findings), scanned, scriptdb.Summary(findings), version)
}

func validateContractVersionStampPrerequisites(version string, manifest map[string]interface{}) error {
	if version != NestedAgentArtifactsContractVersion {
		return nil
	}
	codeLayout, ok := manifest["code_layout_version"].(float64)
	if !ok || codeLayout != 1 {
		return fmt.Errorf("contract %s requires code_layout_version=1; migrate every scripted bundle to code/<step-id>/ and ask the server administrator to complete its source-layout update before stamping", version)
	}
	return nil
}

func authorizeContractVersionStamp(sessionID, version string, nextPending func() (string, string, error)) (string, bool) {
	// An operator working in the workflow builder is the authorization. They
	// asked for the migration and can see what the agent does, so there is
	// nothing to forge. Binding them too removed the only way a person could
	// unblock a stalled upgrade by hand — which is exactly what a workflow that
	// keeps declining its own migration needs.
	//
	// What still has to hold is the ladder. Without checking the next pending
	// target, nothing otherwise stops a stamp jumping straight to the
	// newest version and skipping three migrations whose work was never done.
	if !contractupgrade.IsScheduled(sessionID) {
		if nextPending == nil {
			return "", true
		}
		target, label, err := nextPending()
		if err != nil {
			// A lookup failure is not evidence of a bad stamp; do not block the
			// operator's only manual route on it.
			return "", true
		}
		switch {
		case target == "":
			return "Refused: this workflow owes no contract migration, so there is nothing to stamp. Use get_contract_upgrades to confirm what it is on.", false
		case target != version:
			return fmt.Sprintf(
				"Refused: the next migration this workflow owes is %s (%s), not %s. Migrations are stamped one at a time, in order — stamping %s now would record %s as done without its work being performed. Use get_contract_upgrades, complete %s, then stamp it.",
				target, label, version, version, target, target,
			), false
		}
		return "", true
	}
	if contractupgrade.Consume(sessionID, version) {
		return "", true
	}
	if granted := contractupgrade.Granted(sessionID); granted != "" {
		return fmt.Sprintf(
			"Refused: this turn is authorized to stamp %s, not %s. Stamp the version its own upgrade instruction named.",
			granted, version,
		), false
	}
	return "Refused: schedules may run an older workflow contract, but they never authorize or perform migrations. " +
		"Open the workflow chat and use Update workflow so an operator can supervise each migration in order.", false
}

// CheckContractStamp applies the stamp prerequisites of the Builder's
// set_workflow_contract_version tool for a caller that stamps without a model
// turn (the MCP workflow_upgrade route). Keep it in step with that tool.
// workflowDir is the workflow's absolute folder; readPlan returns
// planning/plan.json.
func CheckContractStamp(version, workflowDir string, manifestJSON []byte, readPlan func() (string, error)) error {
	var manifest map[string]interface{}
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return fmt.Errorf("parse workflow.json: %w", err)
	}
	if err := validateContractVersionStampPrerequisites(version, manifest); err != nil {
		return fmt.Errorf("the nested Agent artifact migration is incomplete: %w", err)
	}
	if err := validateManagedDBScriptsStamp(version, workflowDir); err != nil {
		return fmt.Errorf("the managed database script migration is incomplete: %w", err)
	}
	if version == AgentDescriptionFirstContractVersion {
		content, err := readPlan()
		if err != nil {
			return err
		}
		if err := ValidateAgentDescriptionFirstContractStamp(content); err != nil {
			return err
		}
	}
	if version == WorkflowSkillsContractVersion {
		if err := ValidateWorkflowSkillsStamp(workflowDir); err != nil {
			return err
		}
	}
	if version == ResourceIntentContractVersion {
		content, err := readPlan()
		if err != nil {
			return err
		}
		if err := ValidateResourceIntentContractStamp(content); err != nil {
			return err
		}
	}
	if version == AgentStepContractVersion {
		content, err := readPlan()
		if err != nil {
			return err
		}
		if err := ValidateAgentStepContractStamp(content); err != nil {
			return err
		}
	}
	if err := validateStepDescriptionLayoutStamp(version, workflowDir); err != nil {
		return fmt.Errorf("the step description layout migration is incomplete: %w", err)
	}
	return nil
}

// AuthorizeOperatorContractStamp is the Builder operator path of
// authorizeContractVersionStamp: the signed-in person is the authorization, the
// migration ladder still holds.
func AuthorizeOperatorContractStamp(version string, nextPending func() (string, string, error)) (string, bool) {
	return authorizeContractVersionStamp("", version, nextPending)
}
