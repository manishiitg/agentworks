package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

func externalWorkflowCrewDefinition(add func(string, string, bool, bool, map[string]any, ...string)) error {
	reg := crewCapabilityRegistrar{}
	api := &StreamingAPI{}
	if err := api.registerCrewAttachmentTool(reg, "", "", func(ctx context.Context, _ bool) (context.Context, error) { return ctx, nil }); err != nil {
		return err
	}
	props := reg["manage_crew_attachment"].schema["properties"].(map[string]any)
	// Removing an existing binding grants no new Crew access. New attachments
	// remain in the native Builder until their Crew consent is exposed directly.
	delete(props, "crew_project_id")
	delete(props, "crew_profile_id")
	props["action"].(map[string]any)["enum"] = []any{"list", "detach"}
	add("workflow_crews", "List or detach this workflow's read-only Crew attachments using the native Builder handler. These are workflow-to-Crew bindings; crew_references manages the opposite direction. Detach takes the alias returned by list, removes its live file grants immediately, and leaves plan steps unchanged. Crew deletion still refuses any remaining workflow attachment or Crew step. Requires existing Builder consent and current workflow owner/editor access; no model turn or new permission.", true, true, props, "action")
	return nil
}

func (api *StreamingAPI) externalWorkflowCrewCall(w http.ResponseWriter, r *http.Request, args map[string]any, workflow DiscoveredWorkflow) {
	if workflow.Manifest.Kind == "relay" {
		externalError(w, 400, "wrong_product", "Crew attachments belong to Goals workflows.")
		return
	}
	action := externalArg(args, "action")
	if action != "list" && action != "detach" {
		externalError(w, 400, "invalid_arguments", "Use list or detach; new attachments use the native Builder.")
		return
	}
	claims := GetUserFromContext(r.Context())
	authorize := func(ctx context.Context, _ bool) (context.Context, error) {
		live, err := validateBuilderGrant(ctx, claims, workflow.Manifest.ID, workflow.WorkspacePath)
		if err != nil {
			return nil, errors.New("current workflow owner/editor access and Builder consent are required")
		}
		ctx = context.WithValue(ctx, UserContextKey, live)
		if !externalFileGuard(ctx, false).Allows("workflow.json", action == "detach") {
			return nil, errors.New("the connection's file guard does not allow workflow attachments")
		}
		return ctx, nil
	}
	ctx, err := authorize(r.Context(), action == "list")
	if err != nil {
		externalError(w, 403, "forbidden", err.Error())
		return
	}
	if action == "detach" {
		release, lockErr := teamLock(ctx, workflow.WorkspacePath)
		if lockErr != nil {
			externalFailure(w, lockErr)
			return
		}
		defer release()
		ctx = context.WithValue(ctx, teamLockHeldKey{}, workflow.WorkspacePath)
		// A queued edit cannot retain consent or roles from before the lock.
		ctx, err = authorize(ctx, false)
		if err != nil {
			externalError(w, 403, "forbidden", err.Error())
			return
		}
		current, found, readErr := ReadWorkflowManifest(ctx, workflow.WorkspacePath)
		if readErr != nil || !found || current == nil || current.Kind == "relay" {
			externalError(w, 403, "forbidden", "Current workflow authoring authority changed.")
			return
		}
		if current.Team == nil && api.findRunningTrackedExecutionForWorkspaceWhere(workflow.WorkspacePath, trackedExecutionBlocksNewWorkflowBuilderChat) != nil {
			externalError(w, 409, "workflow_busy", "Wait for the current Builder turn before detaching a Crew.")
			return
		}
	}
	warnings := []string{}
	if action == "detach" {
		warnings = workflowCrewReferenceWarnings(ctx, workflow.WorkspacePath, externalArg(args, "alias"))
	}
	reg := crewCapabilityRegistrar{}
	if err = api.registerCrewAttachmentTool(reg, claims.UserID, workflow.WorkspacePath, authorize); err != nil {
		externalFailure(w, err)
		return
	}
	params := map[string]any{"action": action}
	if alias, ok := args["alias"]; ok {
		params["alias"] = alias
	}
	result, err := reg["manage_crew_attachment"].execute(ctx, params)
	if err != nil {
		externalError(w, 400, "attachment_edit_rejected", err.Error())
		return
	}
	if action == "list" {
		var response map[string]any
		if err = json.Unmarshal([]byte(result), &response); err != nil {
			externalFailure(w, err)
			return
		}
		response["workflow_id"] = workflow.Manifest.ID
		externalJSON(w, response)
		return
	}
	externalJSON(w, map[string]any{"workflow_id": workflow.Manifest.ID, "action": action, "result": result, "warnings": warnings})
}

// Warnings do not broaden plan-file consent or change/refuse plan steps.
func workflowCrewReferenceWarnings(ctx context.Context, root, alias string) []string {
	if !externalFileGuard(ctx, false).Allows("planning/plan.json", false) {
		return []string{"Plan references were not inspected because of the connection's file guard; check remaining alias references before running."}
	}
	raw, found, err := readFileFromWorkspace(ctx, root+"/planning/plan.json")
	var plan struct {
		Steps []json.RawMessage `json:"steps"`
	}
	if err != nil || !found || json.Unmarshal([]byte(raw), &plan) != nil {
		return []string{"Plan references could not be inspected; check remaining alias references before running."}
	}
	warnings := []string{}
	for _, rawStep := range plan.Steps {
		if !strings.Contains(string(rawStep), alias+"/") {
			continue
		}
		var step struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(rawStep, &step) == nil {
			warnings = append(warnings, "Step "+step.ID+" may still read "+alias+"/ paths; those reads no longer resolve.")
		}
		if len(warnings) == 50 {
			warnings = append(warnings, "Additional references may exist; inspect the plan before running.")
			break
		}
	}
	return warnings
}
