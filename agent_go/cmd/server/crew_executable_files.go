package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// The owner's Crew agent edits child definitions through the existing workspace
// writers. Its shell keeps the normal Crew sandbox; no extra folder grants.
func (api *StreamingAPI) registerCrewExecutableFileTools(registrar definitionToolRegistrar, userID, crewRoot string) error {
	return registrar.RegisterCustomTool("crew_workflow_files", "Manage this Crew's own workflows and saved programs. action=list returns exact workspaces and Crew agents. read/write takes workspace_path from that list and a relative file: planning/*, code/*, variables/variables.json, relay.py or relay.structure.json. Write code before the plan that uses it. Reuse crew_agent IDs from the list on agent steps; never copy that agent's conversation. Run an ordinary workflow with run_workflow_trigger; use its returned trigger/run IDs to poll get_workflow_trigger_run. These are owned child definitions, not read-only workflow references.", map[string]interface{}{
		"type": "object", "required": []string{"action"}, "properties": map[string]interface{}{
			"action":         map[string]interface{}{"type": "string", "enum": []string{"list", "read", "write"}},
			"workspace_path": map[string]interface{}{"type": "string"},
			"file":           map[string]interface{}{"type": "string"},
			"content":        map[string]interface{}{"type": "string"},
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
		if action == "list" {
			identity, ok := projectIdentityOf(root)
			if !ok {
				return "", fmt.Errorf("Crew is unavailable")
			}
			record, found := defaultProjectOwners().Lookup(identity.Product, identity.Folder)
			if !found {
				return "", fmt.Errorf("Crew is unavailable")
			}
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/api/crew-executables?crew_id="+url.QueryEscape(record.ProjectID), nil)
			response := &internalResponseCapture{header: http.Header{}}
			api.handleCrewExecutables(response, request)
			if response.status >= 400 {
				return "", fmt.Errorf("%s", strings.TrimSpace(response.body.String()))
			}
			return response.body.String(), nil
		}
		target, _ := args["workspace_path"].(string)
		binding, err := findCrewExecutable(target)
		if err != nil || binding == nil || crewExecutableWorkspace(target) != binding.Root || canonicalCrewWorkspaceRoot(binding.CrewRoot) != root {
			return "", fmt.Errorf("choose a workflow from this Crew's own list")
		}
		manifest, found, err := ReadWorkflowManifest(ctx, binding.Root)
		if err != nil || !found || workflowAccessForManifest(claims, manifest) != WorkflowAccessOwner {
			return "", fmt.Errorf("this workflow is unavailable")
		}
		file, _ := args["file"].(string)
		clean, valid := cleanWorkspaceReadPath(file, false)
		allowed := strings.HasPrefix(clean, "planning/") || strings.HasPrefix(clean, "code/") || clean == "variables/variables.json" || clean == "relay.py" || clean == "relay.structure.json"
		if !valid || !allowed {
			return "", fmt.Errorf("choose a definition file under planning, code or variables; runtime and account files are not part of this editor")
		}
		switch action {
		case "read":
			content, found, err := readFileFromWorkspace(ctx, binding.Root+"/"+clean)
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
			if clean == "planning/plan.json" {
				var plan workflow.PlanningResponse
				if err := json.Unmarshal([]byte(content), &plan); err != nil {
					return "", err
				}
				err = writePlanToWorkspace(ctx, binding.Root, &plan)
			} else {
				if clean == "variables/variables.json" {
					var variables workflow.VariablesManifest
					if err := json.Unmarshal([]byte(content), &variables); err != nil {
						return "", err
					}
					for _, group := range variables.Groups {
						if err := workflow.ValidateInputSetName(group.Name); err != nil {
							return "", err
						}
					}
				}
				err = writeFileToWorkspace(ctx, binding.Root+"/"+clean, content)
			}
			if err != nil {
				return "", err
			}
			return "Saved. The Crew's Steps view will refresh.", nil
		default:
			return "", fmt.Errorf("choose list, read or write")
		}
	}, "work_workflow_reference_tools")
}
