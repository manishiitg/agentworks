package server

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// External (MCP / agentworks CLI) access to workflow functions. The
// connection is the caller: the server stamps it as this signed-in user, so
// a function's allow-list and the run history name who called.
func (api *StreamingAPI) externalWorkflowFunctionCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any, selected DiscoveredWorkflow, access WorkflowAccessLevel) {
	ctx := r.Context()
	claims := GetUserFromContext(ctx)
	manifest := selected.Manifest
	if claims == nil || manifest == nil || access == WorkflowAccessNone {
		externalError(w, 403, "forbidden", "You can no longer use this workflow.")
		return
	}
	if token := claims.AccessToken; token != nil && (!token.AllowsWorkflow(manifest.ID) ||
		(name == "project_functions_call" || name == "project_functions_reply") && !token.Allows("runs:execute")) {
		externalError(w, 403, "forbidden", "This connection cannot run this workflow's functions.")
		return
	}
	switch name {
	case "project_functions_list":
		live := manifest
		if manifest.Kind == "relay" {
			release, root, err := resolveRelayRelease(ctx, selected.WorkspacePath, "")
			if err != nil || verifyRelayRelease(ctx, release, root) != nil {
				externalError(w, 409, "not_published", "Publish this Flow before using its functions.")
				return
			}
			published, _, err := ReadWorkflowManifest(ctx, root)
			if err != nil || published == nil || published.ID != manifest.ID {
				externalError(w, 409, "published_version_unavailable", "Published functions are unavailable.")
				return
			}
			manifest = published
		}
		functions := workflowFunctions(manifest)
		listed := make([]map[string]any, 0, len(functions))
		for _, fn := range functions {
			// can_call lets a screen say "not on this function's list" before a
			// run is tried; project_functions_call still decides.
			canCall := true
			schedule, err := findWorkflowFunctionTrigger(live, fn.Name)
			canCall = err == nil && sharedFunctionCallerAllowed(live, schedule.Function, externalCrewCaller(claims).Stamp)
			listed = append(listed, map[string]any{"name": fn.Name, "description": fn.Description, "input_schema": fn.InputSchema, "can_call": canCall})
		}
		externalJSON(w, map[string]any{"workflow_id": manifest.ID, "functions": listed})
	case "project_functions_calls":
		loadExternalSavedFunctionCalls(ctx)
		crewFunctionCalls.Lock()
		saved := make([]*crewFunctionCall, 0, len(crewFunctionCalls.m))
		for _, call := range crewFunctionCalls.m {
			saved = append(saved, call)
		}
		crewFunctionCalls.Unlock()
		calls := []map[string]interface{}{}
		for _, call := range saved {
			call.mu.Lock()
			match := call.TargetKind == triggerCallerWorkflow && call.TargetID == manifest.ID
			call.mu.Unlock()
			if match {
				calls = append(calls, call.snapshot())
			}
		}
		sort.Slice(calls, func(i, j int) bool { return fmt.Sprint(calls[i]["started_at"]) > fmt.Sprint(calls[j]["started_at"]) })
		calls = calls[:min(len(calls), externalInt(args, "limit", 25))]
		externalJSON(w, map[string]interface{}{"project": manifest.ID, "calls": calls})
	case "project_functions_get_call", "project_functions_reply":
		callID, _ := args["call_id"].(string)
		call := lookupCrewFunctionCall(strings.TrimSpace(callID))
		if call == nil {
			externalError(w, 404, "not_found", "Function call not found.")
			return
		}
		call.mu.Lock()
		mine := call.UserID == claims.UserID && call.CallerKind == triggerCallerUser
		// Shared project members may read its results; pending replies still belong to the caller (PLAT-750-22).
		owned := call.TargetKind == triggerCallerWorkflow && call.TargetID == manifest.ID && (mine || name == "project_functions_get_call")
		call.mu.Unlock()
		if !owned {
			externalError(w, 404, "not_found", "Function call not found.")
			return
		}
		if name == "project_functions_reply" {
			requestID, _ := args["request_id"].(string)
			response, _ := args["response"].(string)
			replyFunctionCallInput(w, call, strings.TrimSpace(requestID), response)
			return
		}
		out := externalWorkflowCallResponse(ctx, call, externalCrewWait(args))
		if !api.externalFunctionReadDetails(w, ctx, args, call, out) {
			return
		}
		out["can_reply"] = mine
		externalJSON(w, out)
	case "project_functions_call":
		fnName, _ := args["function"].(string)
		fn, found := findCrewFunction(workflowFunctions(manifest), fnName)
		if !found {
			externalError(w, 404, "not_found", "The workflow has no function "+strings.TrimSpace(fnName)+"; see functions action=list.")
			return
		}
		if manifest.Kind != "relay" {
			schedule, err := findWorkflowFunctionTrigger(manifest, fn.Name)
			if err != nil || !sharedFunctionCallerAllowed(manifest, schedule.Function, externalCrewCaller(claims).Stamp) {
				externalError(w, 403, "forbidden", "This function does not allow you to call it.")
				return
			}
		}
		callArgs, _ := args["input"].(map[string]interface{})
		if raw, present := args["input"]; present && raw != nil && callArgs == nil {
			externalError(w, 400, "invalid_arguments", "input must be a JSON object.")
			return
		}
		target := triggerTarget{Kind: triggerCallerWorkflow, Path: selected.WorkspacePath, Label: firstNonEmptyTrimmed(manifest.Label, manifest.ID), Manifest: manifest}
		submissionID, _ := args["submission_id"].(string)
		call, err := api.startCrewFunctionCall(context.WithoutCancel(ctx), claims.UserID, externalCrewCaller(claims), target, fn, callArgs, externalCrewCallTimeout, submissionID)
		if err != nil {
			externalError(w, 400, "call_refused", err.Error())
			return
		}
		externalJSON(w, externalWorkflowCallResponse(ctx, call, externalCrewWait(args)))
	default:
		externalError(w, 404, "unknown_tool", "Tool is not exposed by this API.")
	}
}

func externalWorkflowCallResponse(ctx context.Context, call *crewFunctionCall, wait time.Duration) map[string]interface{} {
	out := externalCrewCallResponse(ctx, call, wait)
	if _, running := out["next"]; running {
		out["next"] = fmt.Sprintf("Still running. Call functions action=status with this call_id and wait_seconds=%d: it returns as soon as the call finishes, or after that long with the progress so far, so poll again.", externalCrewMaxWaitSeconds)
	}
	return out
}
