package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// The owner's Crew agent edits child definitions through the existing workspace
// writers. Its shell keeps the normal Crew sandbox; no extra folder grants.
func (api *StreamingAPI) registerCrewExecutableFileTools(registrar definitionToolRegistrar, userID, crewRoot string) error {
	return registrar.RegisterCustomTool("crew_workflow_files", "Create and edit this Crew's saved Python workflows. Read the python-workflows skill first. list returns workflow ID, workspace_path and agents. read takes workspace_path and file. write accepts script.json, Python source and one top-level requirements.txt through the managed writer; requirements do not support include or installer options. edit with operation manage_schedules or manage_webhooks manages schedules and webhooks; describe returns those schemas. Functions use functions with project=workspace_path. Run with functions action=run_trigger and inputs.", map[string]interface{}{
		"type": "object", "required": []string{"action"}, "properties": map[string]interface{}{
			"action":         map[string]interface{}{"type": "string", "enum": []string{"list", "create", "read", "write", "describe", "edit"}},
			"workspace_path": map[string]interface{}{"type": "string"},
			"operation":      map[string]interface{}{"type": "string", "enum": crewPlanOperations},
			"arguments":      map[string]interface{}{"type": "object", "description": "Arguments matching the operation schema returned by describe."},
			"file":           map[string]interface{}{"type": "string"},
			"step_id":        map[string]interface{}{"type": "string", "description": "read planning/plan.json only: return this step."},
			"step_ids":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "read planning/plan.json only: return these steps."},
			"offset":         map[string]interface{}{"type": "integer", "description": "read planning/plan.json only: continue a large reply from next_offset."},
			"content":        map[string]interface{}{"type": "string"},
			"entry_mappings": scriptEntryInputEditsSchema(),
			"name":           map[string]interface{}{"type": "string"},
			"kind":           map[string]interface{}{"type": "string", "enum": []string{"script"}, "description": "create only: always script."},
		},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = internalBotRequestContext(ctx, userID)
		claims := GetUserFromContext(ctx)
		root := canonicalCrewWorkspaceRoot(crewRoot)
		if claims == nil || claims.UserID != userID || root == "" || !crewProjectOwnedByCaller(userID, root) {
			return "", fmt.Errorf("only this Crew's owners can edit its workflows; open your Crew chat or dashboard instead")
		}
		if isExternalCrewDirectCall(ctx) && !api.externalCrewReferenceAllowed(ctx, claims, root, true) {
			return "", fmt.Errorf("this Crew is outside the connection's access")
		}
		action, _ := args["action"].(string)
		if action == "create" || action == "write" || action == "edit" {
			if !workflowPermissionInfoForClaims(claims).CanWriteWorkflows {
				return "", fmt.Errorf("your account cannot edit workflows; ask an admin to restore edit access")
			}
		}
		if action == "describe" {
			operation, _ := args["operation"].(string)
			return crewDescribeOperations(operation)
		}
		if action == "list" || action == "create" {
			identity, ok := projectIdentityOf(root)
			if !ok {
				return "", fmt.Errorf("Crew is unavailable")
			}
			record, found := defaultProjectOwners().Lookup(identity.Product, identity.Folder)
			if !found {
				return "", fmt.Errorf("Crew is unavailable")
			}
			method := http.MethodGet
			body := ""
			if action == "create" {
				method = http.MethodPost
				name, _ := args["name"].(string)
				kind, _ := args["kind"].(string)
				encoded, _ := json.Marshal(map[string]string{"name": name, "kind": kind})
				body = string(encoded)
			}
			request, _ := http.NewRequestWithContext(ctx, method, "/api/crew-executables?crew_id="+url.QueryEscape(record.ProjectID), strings.NewReader(body))
			response := &internalResponseCapture{header: http.Header{}}
			api.handleCrewExecutables(response, request)
			if response.status >= 400 {
				return "", fmt.Errorf("%s", strings.TrimSpace(response.body.String()))
			}
			if action == "list" {
				var result map[string]interface{}
				if err := json.Unmarshal(response.body.Bytes(), &result); err != nil {
					return "", err
				}
				if entries, ok := result["executables"].([]interface{}); ok {
					for _, value := range entries {
						if entry, ok := value.(map[string]interface{}); ok {
							if workspace, ok := entry["workspace_path"].(string); ok {
								entry["code_environment"] = workflow.CodeRuntimeEnvironment(workspace)
							}
						}
					}
				}
				encoded, err := json.Marshal(result)
				return string(encoded), err
			}
			return response.body.String(), nil
		}
		target, _ := args["workspace_path"].(string)
		binding, err := findCrewExecutable(target)
		if err != nil || binding == nil || binding.Kind != "script" || crewExecutableWorkspace(target) != binding.Root || canonicalCrewWorkspaceRoot(binding.CrewRoot) != root {
			return "", fmt.Errorf("choose a workflow from this Crew's own list")
		}
		manifest, found, err := ReadWorkflowManifest(ctx, binding.Root)
		if err != nil || !found || manifest.Kind == "relay" || workflowAccessForManifest(claims, manifest) != WorkflowAccessOwner {
			return "", fmt.Errorf("this workflow is unavailable")
		}
		if action == "edit" || action == "write" {
			if err := refuseCrewOwnerTurnOnMacTwin(ctx, root, userID); err != nil {
				return "", err
			}
		}
		if action == "edit" {
			operation, _ := args["operation"].(string)
			if operation == "manage_schedules" || operation == "manage_webhooks" {
				arguments, ok := args["arguments"].(map[string]interface{})
				if !ok {
					return "", fmt.Errorf("provide arguments matching the operation schema")
				}
				request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/api/crew-workflow-configuration", nil)
				response := &internalResponseCapture{header: http.Header{}}
				child := DiscoveredWorkflow{WorkspacePath: binding.Root, Manifest: manifest}
				if operation == "manage_schedules" {
					api.externalScheduleCall(response, request, arguments, externalScheduleTarget{workflow: &child})
				} else {
					api.externalWorkflowTriggerCall(response, request, arguments, child, WorkflowAccessOwner)
				}
				if response.status >= 400 {
					return "", fmt.Errorf("%s", strings.TrimSpace(response.body.String()))
				}
				return response.body.String(), nil
			}
			return "", fmt.Errorf("edit script.json or Python source with write for a saved script")
		}
		file, _ := args["file"].(string)
		clean, valid := cleanWorkspaceReadPath(file, false)
		allowed := (binding.Kind == "script" && crewScriptEditableFile(clean)) || strings.HasPrefix(clean, "planning/") || strings.HasPrefix(clean, "code/") || clean == "variables/variables.json"
		if !valid || !allowed {
			return "", fmt.Errorf("choose a definition file under planning, code or variables; runtime and account files are not part of this editor")
		}
		if binding.Kind == "script" && (strings.HasPrefix(clean, "planning/") || strings.HasPrefix(clean, "variables/")) {
			return "", fmt.Errorf("edit script.json or main.py; a saved script has one definition")
		}
		switch action {
		case "read":
			sourceRoot := binding.Root
			if binding.Kind == "script" {
				sourceRoot = path.Join(binding.Root, "code")
			}
			content, found, err := readFileFromWorkspace(ctx, sourceRoot+"/"+clean)
			if err != nil {
				return "", err
			}
			return crewReadReply(clean, content, found, args)
		case "write":
			content, ok := args["content"].(string)
			if !ok {
				return "", fmt.Errorf("provide the file content")
			}
			entries, decodeErr := decodeScriptEntryInputEdits(args["entry_mappings"])
			if decodeErr != nil {
				return "", decodeErr
			}
			err = writeCrewScriptFile(ctx, binding, clean, content, entries)
			if err != nil {
				return "", err
			}
			if notes := crewScriptSaveNotes(ctx, binding, clean); notes != nil {
				raw, _ := json.Marshal(notes)
				return "Saved. The Crew's Steps view will refresh.\n" + string(raw), nil
			}
			return "Saved. The Crew's Steps view will refresh.", nil
		default:
			return "", fmt.Errorf("choose list, create, read, describe, edit or write")
		}
	}, "work_workflow_reference_tools")
}

// The edit operations that remain: a workflow's schedules and webhooks.
var crewPlanOperations = []string{"manage_schedules", "manage_webhooks"}

func crewPlanDefinitions() []workflow.WorkflowToolDefinition {
	var definitions []workflow.WorkflowToolDefinition
	// Reuse the public action schemas and handlers; only the target is fixed to this Crew's child.
	add := func(name, description string, _, _ bool, properties map[string]any, required ...string) {
		delete(properties, "project")
		description = strings.ReplaceAll(description, "Pass project.", "The target is this Crew's selected workflow or script.")
		required = slices.DeleteFunc(required, func(key string) bool { return key == "project" })
		if name == "manage_triggers" {
			name = "manage_webhooks"
		}
		schema, _ := json.Marshal(map[string]any{"type": "object", "properties": properties, "required": required})
		definitions = append(definitions, workflow.WorkflowToolDefinition{Name: name, Description: description, InputSchema: schema})
	}
	externalScheduleDefinitions(add)
	externalTriggerDefinitions(add)
	return definitions
}
