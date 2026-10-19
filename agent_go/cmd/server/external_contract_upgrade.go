package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

var contractVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func externalContractUpgradeDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("workflow_upgrade", "Workflow contract upgrades without a model turn, with the same checks as the Builder. action=status lists the migrations this workflow still owes, in order. action=stamp records one completed migration: pass version (the next one owed) and confirm (repeat the workflow_id). The stamp runs the Builder's prerequisite checks and refuses out-of-order versions. A migration's work is done first: for the agent-step migration (1.0.47) call plan action=migrate_agent_steps with empty arguments, then stamp; the Goal-agent migration (1.0.50) is plan action=migrate_goal_agents; the skills migration (1.0.51) is plan action=migrate_workflow_skills; other migrations need builder action=chat, whose native tools are get_contract_upgrades, the named migrate_* tool and set_workflow_contract_version (not this external workflow_upgrade tool). Goals workflows only. Requires Builder consent and a current owner or editor role; changes are not applied while the workflow is running.", true, true, map[string]any{
		"action":  map[string]any{"type": "string", "enum": []any{"status", "stamp"}},
		"version": externalString("stamp: the contract version to record, such as 1.0.47; must be the next one status lists."),
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
		pending := []map[string]string{}
		for _, step := range workflowVersionUpgradePlan(current) {
			pending = append(pending, map[string]string{"version": step.to, "migration": step.label})
		}
		externalJSON(w, map[string]any{"workflow_id": workflow.Manifest.ID, "version": workflowContractVersionForUpgrade(current), "platform_version": WorkflowContractCurrentVersion, "code_layout_version": current.CodeLayoutVersion, "pending": pending, "next": "Do the next migration's work, then workflow_upgrade action=stamp. The agent-step migration (1.0.47) is plan action=migrate_agent_steps with empty arguments; the Goal-agent migration (1.0.50) is plan action=migrate_goal_agents; the skills migration (1.0.51) is plan action=migrate_workflow_skills; other migrations use builder action=chat. Inside Builder chat use get_contract_upgrades, the migration tool named by its instructions and set_workflow_contract_version; workflow_upgrade is for the external MCP caller."})
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
	appliedAt := time.Now().UTC().Format(time.RFC3339)
	current.Version = version
	known := false
	for _, entry := range current.ContractUpgradeHistory {
		known = known || entry.Version == version
	}
	if !known {
		current.ContractUpgradeHistory = append(current.ContractUpgradeHistory, WorkflowContractUpgradeHistoryEntry{Version: version, AppliedAt: appliedAt})
	}
	if err := WriteWorkflowManifest(ctx, rootPath, current); err != nil {
		externalError(w, 502, "stamp_failed", "The contract version could not be saved; nothing was recorded.")
		return
	}
	if api.logger != nil {
		api.logger.Info("workflow contract stamped over MCP workflow=" + workflow.Manifest.ID + " version=" + version + " user=" + live.UserID)
	}
	externalJSON(w, map[string]any{"workflow_id": workflow.Manifest.ID, "version": version, "applied": true, "applied_at": appliedAt})
}
