package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// One lifecycle for Crew Main and external operators. Authentication belongs to
// the calling surface; no synthetic Builder consent is created for Crew chat.
func (api *StreamingAPI) checkAndStampWorkflowContract(ctx context.Context, root string, current *WorkflowManifest, version, reason string) (bool, error) {
	if workflowContractNeedsRecreation(current) {
		return false, fmt.Errorf("%s", oldWorkflowContractMessage)
	}
	if workflowContractVersionForUpgrade(current) == version {
		return false, nil
	}
	pending := workflowVersionUpgradePlan(current)
	if len(pending) == 0 || pending[0].to != version {
		return false, fmt.Errorf("complete the next update shown by status; versions must be stamped in order")
	}
	if strings.TrimSpace(reason) == "" {
		return false, fmt.Errorf("describe the reviewed changes in reason before stamping")
	}
	candidate := *current
	candidate.Version = version
	if err := ValidateManifest(&candidate); err != nil {
		return false, fmt.Errorf("workflow was not stamped: %w", err)
	}
	if err := preflightCrewAttachments(ctx, api.projectActions, GetUserIDFromContext(ctx), root); err != nil {
		return false, fmt.Errorf("workflow was not stamped: %w; check upgrade status for repair_attachments", err)
	}
	read := readWorkflowUpgradeFile
	raw, err := read(ctx, root+"/workflow.json")
	if err != nil {
		return false, err
	}
	checked := []string{}
	if err := stepworkflow.CheckContractStamp(version, root, []byte(raw), func() (string, error) {
		return read(ctx, root+"/planning/plan.json")
	}, func() error {
		var checkErr error
		checked, checkErr = stepworkflow.ValidatePortableWorkflowRuntime(ctx, root, crewResourcesForRun(ctx, api.projectActions), readWorkflowUpgradeFile)
		return checkErr
	}); err != nil {
		return false, err
	}
	if err := writeWorkflowUpgradeField(ctx, root, "version", &candidate); err != nil {
		return false, fmt.Errorf("workflow version could not be saved: %w", err)
	}
	if err := stepworkflow.RecordWorkflowContractUpgrade(ctx, root, workflowContractVersionForUpgrade(current), version, reason, checked, read, writeFileToWorkspace); err != nil {
		return true, fmt.Errorf("workflow version was saved, but its upgrade change log failed: %w", err)
	}
	current.Version = version
	return true, nil
}

func (api *StreamingAPI) crewWorkflowUpgrade(ctx context.Context, binding *crewExecutableBinding, manifest *WorkflowManifest, args map[string]interface{}) (string, error) {
	if binding.Kind != "workflow" || manifest.Kind == "relay" {
		return "", fmt.Errorf("choose this Crew's workflow; saved scripts have no workflow upgrade ladder")
	}
	version, _ := args["version"].(string)
	repair, repairing := args["repair_step"].(map[string]interface{})
	repairAttachments, _ := args["repair_attachments"].(bool)
	repairBrowser, _ := args["repair_browser_mode"].(bool)
	repairs := 0
	for _, enabled := range []bool{repairing, repairAttachments, repairBrowser} {
		if enabled {
			repairs++
		}
	}
	if repairs > 1 || repairs > 0 && version != "" {
		return "", fmt.Errorf("choose one named repair, then check and stamp separately")
	}
	if version == "" && repairs == 0 {
		pending := []map[string]string{}
		for _, step := range workflowVersionUpgradePlan(manifest) {
			pending = append(pending, map[string]string{"version": step.to, "update": step.label, "instruction": step.query})
		}
		status := map[string]any{"workflow_id": binding.ID, "version": workflowContractVersionForUpgrade(manifest), "platform_version": WorkflowContractCurrentVersion, "pending": pending}
		if _, known := workflowContractVersionRank(workflowContractVersionForUpgrade(manifest)); !known {
			status["unsupported_version"] = true
			status["next"] = "This server does not recognize the saved workflow version. Leave it paused and ask this Crew's chat to check it; do not downgrade or stamp it."
		} else if workflowContractNeedsRecreation(manifest) || manifest.CodeLayoutVersion != 1 {
			status["next"] = "This workflow is too old to update automatically. Leave it paused and ask this Crew's chat to recreate it."
		} else if len(pending) > 0 {
			status["next"] = "Complete the next instruction in this Crew's chat, then use upgrade with that version and a reason."
		}
		if len(pending) == 1 && pending[0]["version"] == workflowContractPortableVersion || manifest.Version == workflowContractPortableVersion {
			if err := ValidateManifest(manifest); err != nil {
				status["manifest_check_failure"] = err.Error()
			}
			if mode, needed := workflowBrowserModeRepair(manifest.Capabilities.BrowserMode); needed {
				status["browser_mode_repair"] = map[string]any{"field": "capabilities.browser_mode", "from": manifest.Capabilities.BrowserMode, "to": mode, "next": "Use upgrade with repair_browser_mode=true and a reason, then check status again."}
			}
			if err := preflightCrewAttachments(ctx, api.projectActions, GetUserIDFromContext(ctx), binding.Root); err != nil {
				if _, repairErr := refreshedWorkflowAttachments(GetUserIDFromContext(ctx), manifest.CrewAttachments); repairErr != nil {
					status["attachment_repair_error"] = repairErr.Error()
				} else {
					status["attachment_repair"] = map[string]string{"failure": err.Error(), "next": "Use upgrade with repair_attachments=true and a reason to refresh locations for the same owned target projects, then check status again."}
				}
			}
			steps, err := stepworkflow.ValidatePortableWorkflowRuntime(ctx, binding.Root, crewResourcesForRun(ctx, api.projectActions), readWorkflowUpgradeFile)
			if err != nil {
				status["check_failure"] = err.Error()
			} else {
				status["checked_steps"] = steps
			}
		}
		encoded, err := json.Marshal(status)
		return string(encoded), err
	}
	// The Crew authoring surface has already checked current owner/edit/scope
	// authority. Re-read definitions after the existing workflow mutation lock.
	release, err := teamLock(ctx, binding.Root)
	if err != nil {
		return "", err
	}
	defer release()
	ctx = context.WithValue(ctx, teamLockHeldKey{}, binding.Root)
	current, found, err := ReadWorkflowManifest(ctx, binding.Root)
	if err != nil || !found {
		return "", fmt.Errorf("workflow is unavailable")
	}
	claims := GetUserFromContext(ctx)
	if claims == nil || !crewProjectOwnedByCaller(claims.UserID, binding.CrewRoot) || !workflowPermissionInfoForClaims(claims).CanWriteWorkflows || workflowAccessForManifest(claims, current) != WorkflowAccessOwner {
		return "", fmt.Errorf("workflow edit access changed; ask an admin to restore edit access")
	}
	if isExternalCrewDirectCall(ctx) && !api.externalCrewReferenceAllowed(ctx, claims, binding.CrewRoot, true) {
		return "", fmt.Errorf("this Crew is outside the connection's access")
	}
	reason, _ := args["reason"].(string)
	if repairAttachments || repairBrowser {
		return repairWorkflowManifest(ctx, binding.Root, current, repairAttachments, repairBrowser, reason)
	}
	if repairing {
		pending := workflowVersionUpgradePlan(current)
		if len(pending) == 0 || pending[0].to != workflowContractPortableVersion {
			return "", fmt.Errorf("step repair belongs to the pending portable workflow update; use normal named edits for an updated workflow")
		}
		return stepworkflow.RepairPortableWorkflowStep(ctx, binding.Root, repair, reason, readWorkflowUpgradeFile, writeFileToWorkspace)
	}
	applied, err := api.checkAndStampWorkflowContract(ctx, binding.Root, current, version, reason)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(map[string]any{"workflow_id": binding.ID, "version": version, "applied": applied})
	return string(encoded), err
}

// Keep namespace, current caller and any existing Crew edit view from the tool
// request. Missing files remain missing, rather than falling back to old roots.
func readWorkflowUpgradeFile(ctx context.Context, file string) (string, error) {
	text, found, err := readFileFromWorkspace(ctx, file)
	if err == nil && !found {
		err = os.ErrNotExist
	}
	return text, err
}
