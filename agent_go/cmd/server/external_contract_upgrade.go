package server

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

var contractVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func externalContractUpgradeDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("workflow_upgrade", "Retiring (removed with the old plan runner). action=status lists the contract migrations an older workflow still owes; they are no longer carried out one by one because plan editing is gone. Converting an existing workflow to a Python script is done by the platform operator for now. action=stamp (version, confirm = the workflow_id) still records a version only when the prerequisite and ordering checks accept it.", true, true, map[string]any{
		"action":  map[string]any{"type": "string", "enum": []any{"status", "stamp"}},
		"version": externalString("stamp: the next reviewed target version from status."),
		"confirm": externalString("stamp: repeat the workflow_id to confirm."),
		"reason":  externalString("stamp: describe the completed update; required for the portable workflow contract."),
	}, "action")
}

func (api *StreamingAPI) externalContractUpgradeCall(w http.ResponseWriter, r *http.Request, args map[string]any, workflow DiscoveredWorkflow) {
	claims := GetUserFromContext(r.Context())
	if workflow.Manifest.Kind == "relay" {
		externalError(w, 400, "wrong_product", "Flows have their own contract lifecycle; this route is for Goals workflows.")
		return
	}
	if !externalFileAuthoringAllowed(claims, workflow.Manifest) {
		externalError(w, 403, "forbidden", "Current editable workflow authoring authority is required.")
		return
	}
	live, err := validateBuilderGrant(r.Context(), claims, workflow.Manifest.ID, workflow.WorkspacePath)
	if err != nil {
		externalError(w, 403, "forbidden", "Current editable workflow authoring authority is required.")
		return
	}
	rootPath := workflow.WorkspacePath
	rootClean, e := wf.CleanRelative(rootPath)
	if e != nil || rootClean != rootPath || len(strings.Split(rootPath, "/")) != 2 || !strings.HasPrefix(rootPath, "Workflow/") {
		externalError(w, 400, "invalid_workspace", "One canonical workflow root is required.")
		return
	}
	ctx := context.WithValue(r.Context(), UserContextKey, live)
	action := externalArg(args, "action")
	if action == "status" {
		if args["version"] != nil || args["confirm"] != nil {
			externalError(w, 400, "invalid_arguments", "status takes no version or confirm.")
			return
		}
		current, found, err := ReadWorkflowManifest(ctx, rootPath)
		if err != nil || !found {
			externalError(w, 502, "workflow_unavailable", "Cannot read this workflow's manifest.")
			return
		}
		if workflowContractNeedsRecreation(current) {
			externalJSON(w, map[string]any{"workflow_id": workflow.Manifest.ID, "version": workflowContractVersionForUpgrade(current), "platform_version": WorkflowContractCurrentVersion, "pending": []map[string]string{}, "next": oldWorkflowContractMessage})
			return
		}
		pending := []map[string]string{}
		for _, step := range workflowVersionUpgradePlan(current) {
			pending = append(pending, map[string]string{"version": step.to, "migration": step.label})
		}
		externalJSON(w, map[string]any{"workflow_id": workflow.Manifest.ID, "version": workflowContractVersionForUpgrade(current), "platform_version": WorkflowContractCurrentVersion, "code_layout_version": current.CodeLayoutVersion, "pending": pending, "next": "This workflow keeps running on its current contract. Converting it to a Python script is done by the platform operator for now."})
		return
	}
	if action != "stamp" {
		externalError(w, 400, "invalid_arguments", "action must be status or stamp.")
		return
	}
	version := externalArg(args, "version")
	if !contractVersionPattern.MatchString(version) {
		externalError(w, 400, "invalid_arguments", "version must look like 1.0.47.")
		return
	}
	if externalArg(args, "confirm") != workflow.Manifest.ID {
		externalError(w, 400, "confirmation_required", "Repeat the workflow_id in confirm to record this migration.")
		return
	}
	release, err := teamLock(ctx, rootPath)
	if err != nil {
		externalFailure(w, err)
		return
	}
	defer release()
	ctx = context.WithValue(ctx, teamLockHeldKey{}, rootPath)
	// Re-resolve after the lock: queued calls cannot keep stale consent, access
	// or team membership.
	live, err = validateBuilderGrant(ctx, claims, workflow.Manifest.ID, rootPath)
	if err != nil {
		externalError(w, 403, "forbidden", "Workflow authoring authority changed.")
		return
	}
	ctx = context.WithValue(ctx, UserContextKey, live)
	current, found, err := ReadWorkflowManifest(ctx, rootPath)
	if err != nil || !found || !externalFileAuthoringAllowed(live, current) || current.Kind == "relay" {
		externalError(w, 403, "forbidden", "Current workflow authoring authority changed.")
		return
	}
	if current.Team == nil && api.findRunningTrackedExecutionForWorkspaceWhere(rootPath, trackedExecutionBlocksNewWorkflowBuilderChat) != nil {
		externalError(w, 409, "workflow_busy", "Wait for the current Builder turn before upgrading this workflow.")
		return
	}
	if workflowContractVersionForUpgrade(current) == version {
		externalJSON(w, map[string]any{"workflow_id": workflow.Manifest.ID, "version": version, "applied": false, "note": "Already on this contract version."})
		return
	}
	reason := externalArg(args, "reason")
	if reason == "" && version != workflowContractPortableVersion {
		reason = "Operator completed the next workflow contract update."
	}
	applied, err := api.checkAndStampWorkflowContract(ctx, rootPath, current, version, reason)
	if err != nil {
		externalError(w, 409, "stamp_refused", err.Error())
		return
	}
	if api.logger != nil {
		api.logger.Info("workflow contract stamped over MCP workflow=" + workflow.Manifest.ID + " version=" + version + " user=" + live.UserID)
	}
	externalJSON(w, map[string]any{"workflow_id": workflow.Manifest.ID, "version": version, "applied": applied})
}
