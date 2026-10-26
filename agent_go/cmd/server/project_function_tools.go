package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

func projectFunctionTool(name string) bool {
	switch name {
	case "project_functions_list", "project_functions_call", "project_functions_get_call", "project_functions_reply", "project_functions_calls", "project_functions_define", "project_functions_delete":
		return true
	}
	return false
}

func projectFunctionAuthoringSchema() map[string]interface{} {
	definition := map[string]interface{}{"type": "object", "required": []interface{}{"name", "action"}, "properties": map[string]interface{}{
		"project": externalString("Project ID or name. Omit in its own Builder chat."),
		"name":    externalString("snake_case function name."), "description": map[string]interface{}{"type": "string"},
		"action": map[string]interface{}{"type": "object", "additionalProperties": false, "required": []interface{}{"type"}, "properties": map[string]interface{}{
			"type": map[string]interface{}{"type": "string", "enum": []interface{}{"run", "message"}},
			"step": externalString("Optional saved Goal step."), "tab": externalString("Crew tab; omit for the function's named tab."),
			"conversation": map[string]interface{}{"type": "string", "enum": []interface{}{"fresh"}},
		}},
		"input":           map[string]interface{}{"type": "object", "description": "Fixed action inputs: messages (Crew instructions), groups, routes and payload for a Goal/Flow."},
		"input_schema":    map[string]interface{}{"type": "object", "description": "JSON Schema for callers' input."},
		"result_schema":   map[string]interface{}{"type": "object", "description": "Crew result JSON Schema; Goals return their run outcome."},
		"allowed_callers": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object"}},
		"enabled":         map[string]interface{}{"type": "boolean"},
	}}
	delete(definition["properties"].(map[string]interface{}), "project")
	return map[string]interface{}{"type": "object", "required": []interface{}{"definition"}, "properties": map[string]interface{}{"project": externalString("Project ID or name; omit in its Builder chat."), "definition": definition}}

}

// Both native Builder and external callers use this authoring service. Goal
// adapters retain their declared-variable and route validation; Crew adapters
// retain published-agent ownership and structured-result validation.
func manageProjectFunction(ctx context.Context, claims *UserClaims, target triggerTarget, args map[string]interface{}, remove bool) (map[string]interface{}, error) {
	if !remove {
		definition, ok := args["definition"].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("definition is required: name, action and input contract")
		}
		args = definition
	}
	if claims == nil {
		return nil, fmt.Errorf("sign in to change project functions")
	}
	if target.Kind == triggerCallerWorkflow {
		access, manifest := workflowAccessForWorkspacePath(ctx, claims, target.Path)
		if manifest == nil || access != WorkflowAccessOwner && access != WorkflowAccessWrite {
			return nil, fmt.Errorf("only a project Builder can change its functions")
		}
		target.Manifest = manifest
	} else if !crewProjectOwnedByCaller(claims.UserID, target.Path) {
		return nil, fmt.Errorf("only a project Builder can change its functions")
	}
	name, _ := args["name"].(string)
	name = strings.TrimSpace(name)
	if !workflowFunctionNamePattern.MatchString(name) || name == crewFunctionAskName {
		return nil, fmt.Errorf("choose a snake_case function name; ask is reserved")
	}
	root := target.Path
	if target.Kind == triggerCallerCrew {
		root = crewFunctionRoot(ctx, target)
	}
	records, err := readProjectFunctionDefinitions(ctx, root)
	if err != nil {
		return nil, err
	}
	index := -1
	for i, fn := range records {
		if fn.Name == name {
			index = i
			break
		}
	}
	if index >= 0 && records[index].InputMapping != nil {
		return nil, fmt.Errorf("this function is bound to a saved step; edit that binding in its Builder")
	}
	if remove {
		if index < 0 {
			return nil, fmt.Errorf("this project has no function %q", name)
		}
		records = append(records[:index], records[index+1:]...)
	} else {
		record := projectFunctionDefinition{ID: uuid.NewString(), Name: name, Enabled: true, Input: json.RawMessage(`{}`), CreatedBy: claims.UserID, CreatedAt: time.Now().UTC()}
		if index >= 0 {
			record = records[index]
		}
		// Decode only the declared definition fields, never identity/creator data.
		if raw, ok := args["description"].(string); ok {
			record.Description = raw
		}
		if enabled, ok := args["enabled"].(bool); ok {
			record.Enabled = enabled
		}
		raw, err := json.Marshal(args["action"])
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &record.Action); err != nil {
			return nil, err
		}
		if record.Action.Type != "run" && record.Action.Type != "message" {
			return nil, fmt.Errorf("function action must run or message")
		}
		if input, ok := args["input"]; ok {
			record.Input, err = json.Marshal(input)
			if err != nil {
				return nil, err
			}
		}
		if schema, ok := args["input_schema"].(map[string]interface{}); ok {
			record.InputSchema = schema
		}
		if schema, ok := args["result_schema"].(map[string]interface{}); ok {
			record.ResultSchema = schema
		}
		if callers, ok := args["allowed_callers"]; ok {
			raw, err = json.Marshal(callers)
			if err != nil {
				return nil, err
			}
			if err = json.Unmarshal(raw, &record.AllowedCallers); err != nil {
				return nil, err
			}
		}
		record.UpdatedAt = time.Now().UTC()
		if target.Kind == triggerCallerWorkflow {
			if record.Action.Type != "run" {
				return nil, fmt.Errorf("a Goal or Flow function runs its saved plan")
			}
			view, err := workflowFunctionExecutionView(record)
			if err != nil {
				return nil, err
			}
			if err = validateWorkflowFunctionSpec(view.Function); err != nil {
				return nil, err
			}
			if err = validateWebhookVariableNames(ctx, root, workflowFunctionInputNames(view.Function)); err != nil {
				return nil, err
			}
			view.GroupNames, err = validateScheduleGroupNamesForWorkspace(ctx, root, view.GroupNames)
			if err != nil {
				return nil, err
			}
			if err = validateWebhookPayloadMappings(ctx, root, view.Webhook, view.GroupNames, view.RouteSelections); err != nil {
				return nil, err
			}
		} else {
			if record.Action.Type != "message" {
				return nil, fmt.Errorf("a Crew function sends instructions to its saved tab")
			}
			if len(record.AllowedCallers) > 0 {
				return nil, fmt.Errorf("Crew functions use the project's Share setting")
			}
			fn, err := crewFunctionExecutionView(record)
			if err != nil {
				return nil, err
			}
			spec := crewFunctionSpec{Name: fn.Name, Description: fn.Description, Instructions: fn.Instructions, InputSchema: fn.InputSchema, ResultSchema: fn.ResultSchema, Execution: fn.Execution, Agent: fn.Agent}.normalized()
			if err = spec.validate(); err != nil {
				return nil, err
			}
			// Keep the existing published-agent ownership check, including removal.
			all, err := readCrewFunctions(ctx, target)
			if err != nil {
				return nil, err
			}
			for _, old := range all {
				if old.Name == name && old.publication != nil && old.publication.AgentID != "" {
					return nil, fmt.Errorf("change this function at its published agent source")
				}
			}
		}
		if index >= 0 {
			records[index] = record
		} else {
			records = append(records, record)
		}
	}
	if err = writeProjectFunctionDefinitions(ctx, root, records); err != nil {
		return nil, err
	}
	return map[string]interface{}{"project": target.describe(), "function": name, "removed": remove}, nil
}

// The target comes from the requested project, or the durable call for status
// and replies. Scope checks are target-specific, so a Crew-only token cannot
// read a Goal call merely because both use the same family.
func (api *StreamingAPI) externalProjectFunctionCall(w http.ResponseWriter, r *http.Request, name string, args map[string]interface{}) {
	ctx := r.Context()
	claims := GetUserFromContext(ctx)
	if claims == nil {
		externalError(w, 403, "forbidden", "Sign in to use functions.")
		return
	}
	raw, _ := args["project"].(string)
	if name == "project_functions_get_call" || name == "project_functions_reply" {
		call := lookupCrewFunctionCall(externalArg(args, "call_id"))
		if call == nil {
			externalError(w, 404, "not_found", "Function call not found.")
			return
		}
		call.mu.Lock()
		raw = call.TargetPath
		call.mu.Unlock()
	}
	target, err := resolveTriggerTarget(ctx, claims, raw)
	if err != nil {
		externalError(w, 404, "not_found", "Project not found or not allowed for this connection.")
		return
	}
	write := name == "project_functions_define" || name == "project_functions_delete"
	execute := name == "project_functions_call" || name == "project_functions_reply"
	if token := claims.AccessToken; token != nil {
		if token.ProjectKind != "" {
			kind := "work"
			id := target.CrewID
			if target.Kind == triggerCallerWorkflow {
				kind = "workflow"
				id = target.Manifest.ID
				if target.Manifest.Kind == "relay" {
					kind = "relay"
				}
			}
			if token.ProjectKind != kind || token.ProjectID != id {
				externalError(w, 403, "insufficient_scope", "This connection belongs to a different project.")
				return
			}
		}
		allowed := false
		if target.Kind == triggerCallerCrew {
			allowed = token.AllowsCrew(target.CrewID) && (token.Allows("crews:read") || token.Allows("crews:run"))
			if execute {
				allowed = token.AllowsCrew(target.CrewID) && token.Allows("crews:run")
			}
			if write {
				allowed = token.AllowsCrew(target.CrewID) && token.Allows("crews:write")
			}
		} else {
			allowed = token.AllowsWorkflow(target.Manifest.ID) && (token.Allows("workflows:read") || token.Allows("runs:execute"))
			if execute {
				allowed = token.AllowsWorkflow(target.Manifest.ID) && token.Allows("runs:execute")
			}
			if write {
				allowed = token.AllowsWorkflow(target.Manifest.ID) && (token.BuilderAccess() || target.Manifest.Kind == "relay" && token.RelayBuilderAccess())
			}
		}
		if !allowed {
			externalError(w, 403, "insufficient_scope", "This connection cannot use this function action in that project.")
			return
		}
	}
	if write {
		out, err := manageProjectFunction(ctx, claims, target, args, name == "project_functions_delete")
		if err != nil {
			externalError(w, 400, "function_refused", err.Error())
			return
		}
		externalJSON(w, out)
		return
	}
	if target.Kind == triggerCallerCrew {
		// Resolve again through the Crew service's product/share/token boundaries.
		copied := map[string]interface{}{}
		for k, v := range args {
			copied[k] = v
		}
		copied["crew_id"] = target.CrewID
		api.externalCrewCall(w, r, name, copied)
		return
	}
	access := workflowAccessForManifest(claims, target.Manifest)
	api.externalWorkflowFunctionCall(w, r, name, args, DiscoveredWorkflow{WorkspacePath: target.Path, Manifest: target.Manifest}, access)
}
