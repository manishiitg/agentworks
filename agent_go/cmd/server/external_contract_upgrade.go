package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

var contractVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func externalContractUpgradeDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("workflow_upgrade", "Workflow contract upgrades without a model turn, with the same checks as the Builder. action=status only lists the migrations this workflow still owes, in order. Start an explicit upgrade with action=prepare: it runs the native migrate_agent_steps on raw saved JSON before any plan checker and does not stamp a version. action=stamp records one completed migration: pass version (the next one owed) and confirm (repeat the workflow_id). The stamp runs the Builder's prerequisite checks and refuses out-of-order versions. A migration's work is done first: the agent-step conversion is prepared first, even below 1.0.46; stamp 1.0.47 only when it becomes the next version owed; the Goal-agent migration (1.0.50) is plan action=migrate_goal_agents; the skills migration (1.0.51) is plan action=migrate_workflow_skills; other migrations need builder action=chat, whose native tools are get_contract_upgrades, the named migrate_* tool and set_workflow_contract_version (not this external workflow_upgrade tool). Goals workflows only. Requires Builder consent and a current owner or editor role; changes are not applied while the workflow is running.", true, true, map[string]any{
		"action":  map[string]any{"type": "string", "enum": []any{"status", "prepare", "stamp"}},
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
		if workflowContractNeedsRecreation(current) {
			externalJSON(w, map[string]any{"workflow_id": workflow.Manifest.ID, "version": workflowContractVersionForUpgrade(current), "platform_version": WorkflowContractCurrentVersion, "pending": []map[string]string{}, "next": oldWorkflowContractMessage})
			return
		}
		pending := []map[string]string{}
		for _, step := range workflowVersionUpgradePlan(current) {
			pending = append(pending, map[string]string{"version": step.to, "migration": step.label})
		}
		externalJSON(w, map[string]any{"workflow_id": workflow.Manifest.ID, "version": workflowContractVersionForUpgrade(current), "platform_version": WorkflowContractCurrentVersion, "code_layout_version": current.CodeLayoutVersion, "pending": pending, "next": "Start an explicit upgrade with workflow_upgrade action=prepare, before Builder chat or plan checkers; this converts retired agent records without stamping. Then do the next migration's work and workflow_upgrade action=stamp in order; the Goal-agent migration (1.0.50) is plan action=migrate_goal_agents; the skills migration (1.0.51) is plan action=migrate_workflow_skills; other migrations use builder action=chat. Inside Builder chat use get_contract_upgrades, the migration tool named by its instructions and set_workflow_contract_version; workflow_upgrade is for the external MCP caller."})
		return
	}
	if action == "prepare" {
		if args["version"] != nil || args["confirm"] != nil {
			externalError(w, 400, "invalid_arguments", "prepare takes no version or confirm; it never stamps.")
			return
		}
		// Reuse the native migration's authoring, consent, team, revision and
		// changelog path. This raw-JSON prerequisite must precede typed checkers;
		// the later 1.0.47 stamp still follows the ordinary contract ladder.
		revision, err := externalPlanRevision(ctx, rootPath)
		if err != nil {
			externalFailure(w, err)
			return
		}
		api.externalPlanMutationCall(w, r.WithContext(ctx), map[string]any{
			"action": "migrate_agent_steps", "arguments": map[string]any{},
			"expected_revision": revision, "request_id": "upgrade-prepare-" + uuid.NewString(),
		}, workflow)
		return
	}
	if action != "stamp" {
		externalError(w, 400, "invalid_arguments", "action must be status, prepare or stamp.")
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
