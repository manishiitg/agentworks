package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// The owner's Crew agent edits child definitions through the existing workspace
// writers. Its shell keeps the normal Crew sandbox; no extra folder grants.
func (api *StreamingAPI) registerCrewExecutableFileTools(registrar definitionToolRegistrar, userID, crewRoot string) error {
	return registrar.RegisterCustomTool("crew_workflow_files", "Manage this Crew's workflows and reusable scripts. Read panel-plan references/crew-plan/SKILL.md before creating or editing. list returns exact workspace paths and available Crew agents; create needs name and kind. read/write use workspace_path, file and complete content. Writes validate managed definitions. Run saved work with run_workflow_trigger.", map[string]interface{}{
		"type": "object", "required": []string{"action"}, "properties": map[string]interface{}{
			"action":         map[string]interface{}{"type": "string", "enum": []string{"list", "create", "read", "write"}},
			"workspace_path": map[string]interface{}{"type": "string"},
			"file":           map[string]interface{}{"type": "string"},
			"content":        map[string]interface{}{"type": "string"},
			"name":           map[string]interface{}{"type": "string"},
			"kind":           map[string]interface{}{"type": "string", "enum": []string{"workflow", "script"}},
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
			return response.body.String(), nil
		}
		target, _ := args["workspace_path"].(string)
		binding, err := findCrewExecutable(target)
		if err != nil || binding == nil || (binding.Kind != "workflow" && binding.Kind != "script") || crewExecutableWorkspace(target) != binding.Root || canonicalCrewWorkspaceRoot(binding.CrewRoot) != root {
			return "", fmt.Errorf("choose a workflow from this Crew's own list")
		}
		manifest, found, err := ReadWorkflowManifest(ctx, binding.Root)
		if err != nil || !found || manifest.Kind == "relay" || workflowAccessForManifest(claims, manifest) != WorkflowAccessOwner {
			return "", fmt.Errorf("this workflow is unavailable")
		}
		file, _ := args["file"].(string)
		clean, valid := cleanWorkspaceReadPath(file, false)
		allowed := (binding.Kind == "script" && (clean == "script.json" || clean == "main.py" || strings.HasSuffix(clean, ".py") && !strings.Contains(clean, "/"))) || strings.HasPrefix(clean, "planning/") || strings.HasPrefix(clean, "code/") || clean == "variables/variables.json"
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
					if err := workflow.ValidateWorkflowInputDefinitions(&variables); err != nil {
						return "", err
					}
					if err := workflow.ValidateWorkflowInputReferences(ctx, binding.Root, &variables); err != nil {
						return "", err
					}

				}
				if binding.Kind == "script" {
					err = writeCrewScriptFile(ctx, binding, clean, content)
				} else {
					err = writeFileToWorkspace(ctx, binding.Root+"/"+clean, content)
				}
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
