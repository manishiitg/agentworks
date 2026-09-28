package server

import (
	"context"
	"net/http"
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
	switch name {
	case "list_workflow_functions":
		functions := append(workflowFunctions(manifest), workflowAskFunction())
		listed := make([]map[string]any, 0, len(functions))
		for _, fn := range functions {
			listed = append(listed, map[string]any{"name": fn.Name, "description": fn.Description, "input_schema": fn.InputSchema})
		}
		externalJSON(w, map[string]any{"workflow_id": manifest.ID, "functions": listed})
	case "get_workflow_function_call":
		callID, _ := args["call_id"].(string)
		call := lookupCrewFunctionCall(strings.TrimSpace(callID))
		if call == nil {
			externalError(w, 404, "not_found", "Function call not found.")
			return
		}
		call.mu.Lock()
		owned := call.UserID == claims.UserID && call.CallerKind == triggerCallerUser && call.TargetKind == triggerCallerWorkflow && call.TargetID == manifest.ID
		call.mu.Unlock()
		if !owned {
			externalError(w, 404, "not_found", "Function call not found.")
			return
		}
		out := externalWorkflowCallResponse(ctx, call, 0)
		api.addExternalCallPendingInputs(ctx, call, out)
		externalJSON(w, out)
	case "call_workflow_function":
		if access != WorkflowAccessOwner && access != WorkflowAccessWrite {
			externalError(w, 403, "forbidden", "Calling a workflow function needs owner or editor access to the workflow.")
			return
		}
		fnName, _ := args["function"].(string)
		fn, found := findCrewFunction(append(workflowFunctions(manifest), workflowAskFunction()), fnName)
		if !found {
			externalError(w, 404, "not_found", "The workflow has no function "+strings.TrimSpace(fnName)+"; see list_workflow_functions.")
			return
		}
		callArgs, _ := args["args"].(map[string]interface{})
		target := triggerTarget{Kind: triggerCallerWorkflow, Path: selected.WorkspacePath, Label: firstNonEmptyTrimmed(manifest.Label, manifest.ID), Manifest: manifest}
		call, err := api.startCrewFunctionCall(context.WithoutCancel(ctx), claims.UserID, externalCrewCaller(claims), target, fn, callArgs, externalCrewCallTimeout)
		if err != nil {
			externalError(w, 400, "call_refused", err.Error())
			return
		}
		out := externalWorkflowCallResponse(ctx, call, externalCrewWait(args))
		api.addExternalCallPendingInputs(ctx, call, out)
		externalJSON(w, out)
	default:
		externalError(w, 404, "unknown_tool", "Tool is not exposed by this API.")
	}
}

func externalWorkflowCallResponse(ctx context.Context, call *crewFunctionCall, wait time.Duration) map[string]interface{} {
	out := externalCrewCallResponse(ctx, call, wait)
	if _, running := out["next"]; running {
		out["next"] = "Still running. Poll get_workflow_function_call with this call_id."
	}
	return out
}
