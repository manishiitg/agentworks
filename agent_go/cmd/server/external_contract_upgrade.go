package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

var contractVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func externalContractUpgradeDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("workflow_upgrade", "Temporary one-time upgrades for older Goals; removed once every Goal has moved. action=status lists the remaining migrations in order. Review and complete each migration through Builder before action=stamp, passing version and confirm (repeat the workflow_id). The stamp uses the same prerequisite and ordering checks as Builder. The remaining skills copy is plan action=migrate_workflow_skills. Saved step-shape preparation is performed by deployment. Goals only, with existing Builder consent and current owner/editor checks.", true, true, map[string]any{
		"action":  map[string]any{"type": "string", "enum": []any{"status", "stamp"}},
		"version": externalString("stamp: the next reviewed target version from status."),
		"confirm": externalString("stamp: repeat the workflow_id to confirm."),
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
		externalJSON(w, map[string]any{"workflow_id": workflow.Manifest.ID, "version": workflowContractVersionForUpgrade(current), "platform_version": WorkflowContractCurrentVersion, "code_layout_version": current.CodeLayoutVersion, "pending": pending, "next": "Temporary upgrade for older Goals: complete the migration status lists, then stamp its version in order. Use Builder to review descriptions and resource intent; the skills copy uses plan action=migrate_workflow_skills. Step-shape preparation is part of deployment. Inside Builder use get_contract_upgrades and set_workflow_contract_version."})
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
	workflowDir := filepath.Join(getWorkspaceDocsAbsPath(), filepath.FromSlash(rootPath))
	rawManifest, err := os.ReadFile(filepath.Join(workflowDir, "workflow.json"))
	if err != nil {
		externalError(w, 502, "workflow_unavailable", "Cannot read this workflow's manifest.")
		return
	}
	readPlan := func() (string, error) {
		raw, err := os.ReadFile(filepath.Join(workflowDir, "planning", "plan.json"))
		return string(raw), err
	}
	if err := stepworkflow.CheckContractStamp(version, workflowDir, rawManifest, readPlan); err != nil {
		externalError(w, 409, "stamp_refused", "Refused: "+err.Error())
		return
	}
	if refusal, ok := stepworkflow.AuthorizeOperatorContractStamp(version, func() (string, string, error) {
		return nextWorkflowContractUpgrade(ctx, rootPath)
	}); !ok {
		externalError(w, 409, "stamp_refused", refusal)
		return
	}
	current.Version = version
	if err := WriteWorkflowManifest(ctx, rootPath, current); err != nil {
		externalError(w, 502, "stamp_failed", "The contract version could not be saved; nothing was recorded.")
		return
	}
	if api.logger != nil {
		api.logger.Info("workflow contract stamped over MCP workflow=" + workflow.Manifest.ID + " version=" + version + " user=" + live.UserID)
	}
	externalJSON(w, map[string]any{"workflow_id": workflow.Manifest.ID, "version": version, "applied": true})
}
