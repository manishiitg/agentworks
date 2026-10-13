package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	crewPublishedCallToolName = "call_published_function"
	crewPublishedGetToolName  = "get_published_function_call"
)

// registerCrewPublishedFunctionTools gives a non-owner's chat or ask with a Crew its one way to act (856-59, owner
// 2026-10-13): call a function the Crew's owner published. It is the same call the external call_crew_function tool makes for
// the same person: the person must still be able to use the Crew, the function runs as its own isolated execution with the
// owner's authority (the owner chose to publish it), and the call is attributed to the person. Only this Crew's functions are
// reachable (never another Crew's or a workflow's) and a call has no auto-notification, so the chat polls with the second tool.
func (api *StreamingAPI) registerCrewPublishedFunctionTools(registrar definitionToolRegistrar, callerID, sessionID, workspacePath string) error {
	if api == nil || api.agentProfiles == nil {
		return nil
	}
	crewOf := func(ctx context.Context) (*UserClaims, crewProjectBinding, productProjectManifest, string, error) {
		claims := userClaimsForDirectoryID(strings.TrimSpace(callerID))
		if claims == nil {
			return nil, crewProjectBinding{}, productProjectManifest{}, "", fmt.Errorf("your account cannot call this Crew's functions")
		}
		manifest, err := readCrewProjectManifests(ctx, crewProfileID, strings.TrimSuffix(strings.TrimSpace(workspacePath), "/"))
		if err != nil {
			return nil, crewProjectBinding{}, productProjectManifest{}, "", fmt.Errorf("this Crew's functions are unavailable")
		}
		crew, resolved, summary, ok := api.externalCrewResolve(ctx, claims, manifest.ID)
		if !ok {
			return nil, crewProjectBinding{}, productProjectManifest{}, "", fmt.Errorf("you can no longer use this Crew")
		}
		return claims, crew, resolved, fmt.Sprint(summary["name"]), nil
	}
	if err := registrar.RegisterCustomTool(crewPublishedCallToolName,
		"Call one of the functions this Crew's owner published (see list_functions). The function runs on its own, with the owner's authority, and may write; you cannot. Arguments are validated against its input schema. Returns status=running and a call_id: poll with "+crewPublishedGetToolName+" (wait_seconds up to 25) until it settles, then tell the user the result. Reuse submission_id after an uncertain retry; never call twice for the same work.",
		map[string]interface{}{
			"type": "object", "required": []string{"function"}, "additionalProperties": false,
			"properties": map[string]interface{}{
				"function":      map[string]interface{}{"type": "string", "description": "Function name from list_functions."},
				"args":          map[string]interface{}{"type": "object", "description": "Arguments matching the function's input schema."},
				"submission_id": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 128, "description": "Stable ID for this intended call; reuse it after an uncertain retry."},
				"wait_seconds":  map[string]interface{}{"type": "integer", "minimum": 0, "maximum": externalCrewMaxWaitSeconds, "description": "Wait up to this long for the result before returning status=running (default 0)."},
			},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			claims, crew, manifest, label, err := crewOf(ctx)
			if err != nil {
				return "", err
			}
			target := triggerTarget{Kind: triggerCallerCrew, Path: crew.Binding.WorkspacePath, Label: label, CrewID: manifest.ID, CrewProfile: crewProfileID, CrewOwner: crew.OwnerID}
			functions, err := readCrewFunctions(ctx, target)
			if err != nil {
				return "", fmt.Errorf("cannot read the Crew's functions")
			}
			name, _ := args["function"].(string)
			fn, found := findCrewFunction(offeredCrewFunctions(ctx, target, functions), name)
			if !found {
				return "", fmt.Errorf("this Crew has no published function %q; see list_functions", strings.TrimSpace(name))
			}
			if isBuiltinConversationalFunction(target, fn) {
				return "", fmt.Errorf("%q is the Crew's built-in conversation, not a published function: you are already in a conversation with this Crew", fn.Name)
			}
			callArgs, _ := args["args"].(map[string]interface{})
			if raw, present := args["args"]; present && raw != nil && callArgs == nil {
				return "", fmt.Errorf("args must be a JSON object")
			}
			submissionID, _ := args["submission_id"].(string)
			// A call outlives this chat turn, so it must not be cancelled with it.
			call, err := api.startCrewFunctionCall(context.WithoutCancel(ctx), claims.UserID, externalCrewCaller(claims), target, fn, callArgs, externalCrewCallTimeout, submissionID)
			if err != nil {
				return "", err
			}
			wait := time.Duration(0)
			if raw, ok := args["wait_seconds"].(float64); ok && raw > 0 {
				wait = time.Duration(min(int(raw), externalCrewMaxWaitSeconds)) * time.Second
			}
			return crewPublishedJSON(externalCrewCallResponse(ctx, call, wait))
		}, "workspace_advanced"); err != nil {
		return err
	}
	return registrar.RegisterCustomTool(crewPublishedGetToolName,
		"Read a call you started with "+crewPublishedCallToolName+": status, result, errors. Pass wait_seconds (up to 25) to wait for it; it returns as soon as the call finishes.",
		map[string]interface{}{
			"type": "object", "required": []string{"call_id"}, "additionalProperties": false,
			"properties": map[string]interface{}{
				"call_id":      map[string]interface{}{"type": "string", "minLength": 1},
				"wait_seconds": map[string]interface{}{"type": "integer", "minimum": 0, "maximum": externalCrewMaxWaitSeconds},
			},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			claims, _, manifest, _, err := crewOf(ctx)
			if err != nil {
				return "", err
			}
			id, _ := args["call_id"].(string)
			call := lookupCrewFunctionCall(id)
			if call == nil {
				return "", fmt.Errorf("call not found")
			}
			call.mu.Lock()
			mine := call.UserID == claims.UserID && call.CallerKind == triggerCallerUser && call.TargetKind == triggerCallerCrew && call.TargetID == manifest.ID
			call.mu.Unlock()
			if !mine {
				return "", fmt.Errorf("call not found")
			}
			wait := time.Duration(0)
			if raw, ok := args["wait_seconds"].(float64); ok && raw > 0 {
				wait = time.Duration(min(int(raw), externalCrewMaxWaitSeconds)) * time.Second
			}
			return crewPublishedJSON(externalCrewCallResponse(ctx, call, wait))
		}, "workspace_advanced")
}

func crewPublishedJSON(v map[string]interface{}) (string, error) {
	out, err := json.Marshal(v)
	return string(out), err
}
