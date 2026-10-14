package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
)

// The app/Ask bridge and external MCP use the same definitions and execution
// path. Native tools bind the workflow on the server rather than accepting an ID.
func workflowPublishedFunctionDefinitions(add func(string, string, bool, map[string]any, ...string)) {
	add("list_workflow_functions", "List the workflow's functions: typed entry points (a route plus named inputs set as that run's variables) that callers invoke with call_workflow_function.", false, nil)
	add("call_workflow_function", "Call one of the workflow's functions (see list_workflow_functions). Inputs are checked first: a missing, unknown or mistyped input is refused before anything runs. Returns at once with status=running and a call_id for get_workflow_function_call (functions take minutes); pass wait_seconds to wait up to 25s for the run outcome (status, error, step outputs). Repeating the same call while it runs returns the same call_id. Requires the runs:execute scope and current workflow access. The function’s own caller policy still applies.", true, map[string]any{
		"function":      externalString("Function name from list_workflow_functions."),
		"args":          map[string]any{"type": "object", "description": "Inputs matching the function's input schema."},
		"wait_seconds":  map[string]any{"type": "integer", "minimum": 0, "maximum": externalCrewMaxWaitSeconds, "description": "Seconds to wait for the outcome before returning a call_id to poll (default 0: return at once; max 25)."},
		"submission_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "description": "Stable ID for this intended call. Reuse it to retry uncertain delivery and receive the same call_id, including after completion or restart."},
	}, "function")
	add("get_workflow_function_call", "Poll a call started with call_workflow_function: status, outcome, errors and pending_inputs. MCP clients with elicitation support may receive a question form; otherwise answer a pending request_id with reply_workflow_function_call.", false, map[string]any{"call_id": externalString("call_id returned by call_workflow_function."), "wait_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": externalCrewMaxWaitSeconds, "description": "Wait up to this long for the call to finish; it returns as soon as it does, so a poll costs little. Default: return at once."}}, "call_id")
	add("reply_workflow_function_call", "Answer a pending question on this workflow function call. Use the request_id from get_workflow_function_call. Requires runs:execute and current workflow access. The function’s own caller policy still applies.", true, map[string]any{"call_id": externalString("Call ID from call_workflow_function."), "request_id": externalString("Pending question ID."), "response": externalString("Answer or exact listed choice.")}, "call_id", "request_id", "response")
}

func (api *StreamingAPI) registerWorkflowPublishedFunctionTools(ctx context.Context, registrar definitionToolRegistrar, callerID, sessionID, workspace string) error {
	actor := userClaimsForDirectoryID(strings.TrimSpace(callerID))
	if claims := GetUserFromContext(ctx); claims != nil {
		copy := *claims
		if claims.AccessToken != nil {
			token := *claims.AccessToken
			copy.AccessToken = &token
		}
		actor = &copy
	}
	var registerErr error
	workflowPublishedFunctionDefinitions(func(name, description string, execute bool, properties map[string]any, required ...string) {
		if registerErr != nil {
			return
		}
		if properties == nil {
			properties = map[string]any{}
		}
		schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			schema["required"] = required
		}
		registerErr = registrar.RegisterCustomTool(name, description, schema, func(ctx context.Context, args map[string]any) (string, error) {
			if actor == nil || actor.UserID != strings.TrimSpace(callerID) {
				return "", fmt.Errorf("your account cannot use this workflow")
			}
			// Preserve captured token/folder bounds; a later tool context must
			// never replace a restricted connection with a full account.
			claims := *actor
			if userClaimsForDirectoryID(claims.UserID) == nil || userAccessForClaims(&claims).Disabled {
				return "", fmt.Errorf("your account cannot use this workflow")
			}
			access, manifest := workflowAccessForWorkspacePath(ctx, &claims, workspace)
			if manifest == nil || manifest.Kind == "relay" || access == WorkflowAccessNone {
				return "", fmt.Errorf("you can no longer use this workflow")
			}
			if token := claims.AccessToken; token != nil {
				if !token.Allows("runs:execute") || !token.AllowsWorkflow(manifest.ID) ||
					!accessTokenSessionAllowed(ctx, accessTokenSession{tokenID: token.ID, sessionID: sessionID, workspace: workspace, access: access}) {
					return "", fmt.Errorf("this connection can no longer ask or run this workflow")
				}
			}
			// This is the existing typed-call API, also used by MCP. It checks
			// arguments, per-function caller policy, idempotency and call ownership.
			req := httptest.NewRequest(http.MethodPost, "/api/external/call", nil)
			req = req.WithContext(context.WithValue(ctx, UserContextKey, &claims))
			rec := httptest.NewRecorder()
			api.externalWorkflowFunctionCall(rec, req, name, args, DiscoveredWorkflow{WorkspacePath: workspace, Manifest: manifest}, access)
			if rec.Code >= 300 {
				return "", fmt.Errorf("%s", strings.TrimSpace(rec.Body.String()))
			}
			return rec.Body.String(), nil
		}, "workspace_advanced")
	})
	return registerErr
}
