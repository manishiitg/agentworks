package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// The owner's Crew agent edits child definitions through the existing workspace
// writers. Its shell keeps the normal Crew sandbox; no extra folder grants.
func (api *StreamingAPI) registerCrewExecutableFileTools(registrar definitionToolRegistrar, userID, crewRoot string) error {
	return registrar.RegisterCustomTool("crew_workflow_files", "Create and edit this Crew's single workflow and reusable Python scripts. Use routes in the existing workflow for different tasks; list before creating. list returns workflow ID, workspace_path and agents. upgrade takes workflow_id: omit version to see status, the next update instructions and current check failures; supply version and reason after completing them to check and stamp. A failed check never stamps. read takes workspace_path and file. edit changes named steps or inputs, or manages schedules/webhooks; describe gives schemas. Functions use functions with project=workspace_path. Omitted steps stay unchanged; deletion is explicit. write is for code/script.json, never whole plans/config/inputs. Run with functions action=run_trigger and inputs.", map[string]interface{}{
		"type": "object", "required": []string{"action"}, "properties": map[string]interface{}{
			"action":              map[string]interface{}{"type": "string", "enum": []string{"list", "create", "read", "write", "describe", "edit", "upgrade"}},
			"workflow_id":         map[string]interface{}{"type": "string"},
			"version":             map[string]interface{}{"type": "string"},
			"reason":              map[string]interface{}{"type": "string"},
			"repair_step":         map[string]interface{}{"type": "object", "description": "upgrade only: repair one malformed old step with step_id, changes and optional remove_fields. Unmentioned fields and other steps remain unchanged."},
			"repair_attachments":  map[string]interface{}{"type": "boolean", "description": "upgrade only: refresh moved Crew connection locations for the same target project IDs owned by the caller. Check status first; no access or version change."},
			"repair_browser_mode": map[string]interface{}{"type": "boolean", "description": "upgrade only: convert a retired browser mode using the installation's existing migration policy. Check status first; no account or version change."},
			"workspace_path":      map[string]interface{}{"type": "string"},
			"operation":           map[string]interface{}{"type": "string", "enum": crewPlanOperations},
			"arguments":           map[string]interface{}{"type": "object", "description": "Arguments matching the operation schema returned by describe."},
			"file":                map[string]interface{}{"type": "string"},
			"content":             map[string]interface{}{"type": "string"},
			"name":                map[string]interface{}{"type": "string"},
			"kind":                map[string]interface{}{"type": "string", "enum": []string{"workflow", "script"}},
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
		if action == "create" || action == "write" || action == "edit" || action == "upgrade" {
			if !workflowPermissionInfoForClaims(claims).CanWriteWorkflows {
				return "", fmt.Errorf("your account cannot edit workflows; ask an admin to restore edit access")
			}
		}
		if action == "describe" {
			operation, _ := args["operation"].(string)
			definitions := crewPlanDefinitions()
			if operation != "" {
				definitions = slices.DeleteFunc(definitions, func(d workflow.ExternalPlanToolDefinition) bool { return d.Name != operation })
				if len(definitions) == 0 {
					return "", fmt.Errorf("choose a supported editing operation")
				}
			}
			encoded, err := json.Marshal(definitions)
			return string(encoded), err
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
		if action == "upgrade" {
			id, _ := args["workflow_id"].(string)
			binding, err := findCrewExecutableByID(id)
			if err != nil || binding == nil || canonicalCrewWorkspaceRoot(binding.CrewRoot) != root {
				return "", fmt.Errorf("choose a workflow ID from this Crew's own list")
			}
			target = binding.Root
		}
		binding, err := findCrewExecutable(target)
		if err != nil || binding == nil || (binding.Kind != "workflow" && binding.Kind != "script") || crewExecutableWorkspace(target) != binding.Root || canonicalCrewWorkspaceRoot(binding.CrewRoot) != root {
			return "", fmt.Errorf("choose a workflow from this Crew's own list")
		}
		manifest, found, err := ReadWorkflowManifest(ctx, binding.Root)
		if err != nil || !found || manifest.Kind == "relay" || workflowAccessForManifest(claims, manifest) != WorkflowAccessOwner {
			return "", fmt.Errorf("this workflow is unavailable")
		}
		if action == "edit" || action == "write" || action == "upgrade" {
			if err := refuseCrewOwnerTurnOnMacTwin(ctx, root, userID); err != nil {
				return "", err
			}
		}
		if action == "upgrade" {
			return api.crewWorkflowUpgrade(ctx, binding, manifest, args)
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
			if binding.Kind != "workflow" {
				return "", fmt.Errorf("edit script.json or Python source with write for a saved script")
			}
			if !slices.Contains(crewPlanOperations, operation) {
				return "", fmt.Errorf("choose a supported editing operation; describe lists their schemas")
			}
			arguments, ok := args["arguments"].(map[string]interface{})
			if !ok {
				return "", fmt.Errorf("provide arguments matching the operation schema")
			}
			read := func(callCtx context.Context, file string) (string, error) {
				content, found, err := readFileFromWorkspace(callCtx, file)
				if err == nil && !found {
					err = os.ErrNotExist
				}
				return content, err
			}
			ctx = workflow.WithExternalPlanSelectedServers(ctx, manifest.Capabilities.RuntimeServers())
			if operation == "update_variable" {
				return workflow.ExecuteWorkflowInputEdit(ctx, arguments, binding.Root, api.logger, read, writeFileToWorkspace)
			}
			return workflow.ExecuteExternalBuilderPlanTool(ctx, operation, arguments, binding.Root, api.logger, read, writeFileToWorkspace, nil)
		}
		file, _ := args["file"].(string)
		clean, valid := cleanWorkspaceReadPath(file, false)
		pending := workflowVersionUpgradePlan(manifest)
		upgrading := binding.Kind == "workflow" && len(pending) > 0 && !workflowContractNeedsRecreation(manifest) && manifest.CodeLayoutVersion == 1
		guide := upgrading && strings.HasSuffix(clean, ".md") && (strings.HasPrefix(clean, "skills/") || strings.HasPrefix(clean, "soul/"))
		oldHelperRead := upgrading && action == "read" && strings.HasPrefix(clean, "learnings/")
		allowed := guide || oldHelperRead || (binding.Kind == "script" && (clean == "script.json" || clean == "main.py" || strings.HasSuffix(clean, ".py") && !strings.Contains(clean, "/"))) || strings.HasPrefix(clean, "planning/") || strings.HasPrefix(clean, "code/") || clean == "variables/variables.json"
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
			encoded, err := json.Marshal(map[string]interface{}{"file": clean, "exists": found, "content": content})
			return string(encoded), err
		case "write":
			content, ok := args["content"].(string)
			if !ok {
				return "", fmt.Errorf("provide the file content")
			}
			if binding.Kind == "workflow" && !strings.HasPrefix(clean, "code/") && !guide {
				return "", fmt.Errorf("use edit with a named step or input operation; whole plan/config/input files cannot be replaced")
			}
			if binding.Kind == "script" {
				err = writeCrewScriptFile(ctx, binding, clean, content)
			} else {
				err = writeFileToWorkspace(ctx, binding.Root+"/"+clean, content)
			}
			if err != nil {
				return "", err
			}
			return "Saved. The Crew's Steps view will refresh.", nil
		default:
			return "", fmt.Errorf("choose list, create, read, describe, edit or write")
		}
	}, "work_workflow_reference_tools")
}

// One editing vocabulary, backed by the same native handlers as the Builder.
var crewPlanOperations = []string{"create_plan", "add_step", "update_step", "delete_plan_steps", "manage_step_route", "update_step_config", "update_validation_schema", "update_variable", "manage_schedules", "manage_webhooks"}

func crewPlanDefinitions() []workflow.ExternalPlanToolDefinition {
	definitions := workflow.ExternalBuilderPlanToolDefinitions()
	definitions = slices.DeleteFunc(definitions, func(d workflow.ExternalPlanToolDefinition) bool { return !slices.Contains(crewPlanOperations, d.Name) })
	definitions = append(definitions, workflow.WorkflowInputEditDefinition())
	// Reuse the public action schemas and handlers; only the target is fixed to this Crew's child.
	add := func(name, description string, _, _ bool, properties map[string]any, required ...string) {
		delete(properties, "project")
		description = strings.ReplaceAll(description, "Pass project.", "The target is this Crew's selected workflow or script.")
		required = slices.DeleteFunc(required, func(key string) bool { return key == "project" })
		if name == "manage_triggers" {
			name = "manage_webhooks"
		}
		schema, _ := json.Marshal(map[string]any{"type": "object", "properties": properties, "required": required})
		definitions = append(definitions, workflow.ExternalPlanToolDefinition{Name: name, Description: description, InputSchema: schema})
	}
	externalScheduleDefinitions(add)
	externalTriggerDefinitions(add)
	return definitions
}
