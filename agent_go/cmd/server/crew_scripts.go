package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"reflect"
	"strings"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// A typed create writes the initial definition before the derived manifest can
// exist. This in-process permit covers only that new script's first JSON file.
type crewScriptCreationKey struct{}

func initializeCrewScriptSource(ctx context.Context, binding crewExecutableBinding, name string) error {
	definition := workflow.CrewScriptDefinition{ID: binding.ID, Name: name, Outputs: []string{"result.json"}}
	encoded, _ := json.MarshalIndent(definition, "", "  ")
	sourceRoot := path.Join(binding.Root, "code")
	if err := writeFileToWorkspace(context.WithValue(ctx, crewScriptCreationKey{}, binding.Root), sourceRoot+"/script.json", string(encoded)); err != nil {
		return err
	}
	return writeFileToWorkspace(ctx, sourceRoot+"/main.py", "import json, os\nfrom pathlib import Path\ninputs = json.loads(os.environ.get('STEP_PARAMS_JSON', '{}'))\nPath(os.environ['STEP_OUTPUT_DIR'], 'result.json').write_text(json.dumps(inputs))\n")
}

func readCrewScript(ctx context.Context, root string) (*workflow.CrewScriptDefinition, error) {
	if binding, err := findCrewExecutable(root); err != nil {
		return nil, err
	} else if binding != nil && binding.Kind == "script" {
		root = path.Join(binding.Root, "code")
	}
	return workflow.ReadCrewScriptDefinition(ctx, root, func(ctx context.Context, file string) (string, error) {
		raw, found, err := readFileFromWorkspace(ctx, file)
		if err != nil {
			return "", err
		}
		if !found {
			return "", fmt.Errorf("saved script is unavailable")
		}
		return raw, nil
	})
}

func crewScriptManifest(ctx context.Context, binding *crewExecutableBinding) (*WorkflowManifest, bool, error) {
	definition, err := readCrewScript(ctx, binding.Root)
	if err != nil {
		return nil, false, err
	}
	if definition.ID != binding.ID {
		return nil, false, fmt.Errorf("script identity does not match its Crew binding")
	}
	owner, ok := crewProjectOwnerID(binding.CrewRoot)
	if !ok {
		return nil, false, fmt.Errorf("owning Crew is unavailable")
	}
	manifest := NewWorkflowManifest(definition.Name)
	manifest.ID = binding.ID
	manifest.Description = definition.Description
	manifest.CreatedBy = owner
	manifest.Access = &WorkflowAccess{Owners: []string{owner}}
	manifest.crewExecutable = binding
	manifest.Capabilities, err = crewScriptCapabilities(ctx, binding.CrewRoot, definition)
	if err != nil {
		return nil, false, err
	}

	if err := loadWorkflowEntryActions(ctx, binding.Root, manifest); err != nil {
		return nil, false, err
	}
	if err := loadWorkflowTimerActions(ctx, binding.Root, manifest); err != nil {
		return nil, false, err
	}
	return manifest, true, nil
}

// Saved scripts inherit the Crew's existing settings. Explicit overrides use
// the same capability schema and validation as workflows, not a new authority.
func crewScriptCapabilities(ctx context.Context, crewRoot string, definition *workflow.CrewScriptDefinition) (WorkflowCapabilities, error) {
	candidate := NewWorkflowManifest(definition.Name)
	if parent, err := readCrewProjectManifests(ctx, "work", crewRoot); err == nil {
		raw, err := json.Marshal(parent.Capabilities)
		if err != nil {
			return WorkflowCapabilities{}, err
		}
		if err := json.Unmarshal(raw, &candidate.Capabilities); err != nil {
			return WorkflowCapabilities{}, err
		}
	}
	if len(definition.Capabilities) != 0 {
		if !strings.HasPrefix(strings.TrimSpace(string(definition.Capabilities)), "{") {
			return WorkflowCapabilities{}, fmt.Errorf("script capabilities must be a settings object")
		}
		if err := json.Unmarshal(definition.Capabilities, &candidate.Capabilities); err != nil {
			return WorkflowCapabilities{}, fmt.Errorf("read script settings: %w", err)
		}
	}
	if err := ValidateManifest(candidate); err != nil {
		return WorkflowCapabilities{}, err
	}
	return candidate.Capabilities, nil
}

// Script ids are configuration, never derived from mapped values. Resolve source
// only through the server's trusted executable-to-Crew ownership registry.
func (r *crewWorkflowResources) ResolveCrewScript(ctx context.Context, workspace, id string) (workflow.CrewScriptConfiguration, error) {
	var result workflow.CrewScriptConfiguration
	parent, err := findCrewExecutable(workspace)
	if id == "" && err == nil && (parent == nil || parent.Kind != "script") {
		return result, nil
	}
	if err != nil || parent == nil {
		return result, fmt.Errorf("this workflow has no owning Crew")
	}
	owner, ok := crewProjectOwnerID(parent.CrewRoot)
	if !ok || owner != r.userID {
		return result, fmt.Errorf("this Crew's execution owner is unavailable")
	}
	bindings, err := readCrewExecutables()
	if err != nil {
		return result, err
	}
	if id == "" {
		if parent.Kind != "script" {
			return result, nil
		}
		id = parent.ID
	}
	id = strings.TrimPrefix(id, "script:")
	for _, binding := range bindings {
		if binding.ID != id || binding.Kind != "script" {
			continue
		}
		if canonicalCrewWorkspaceRoot(binding.CrewRoot) != canonicalCrewWorkspaceRoot(parent.CrewRoot) {
			return result, fmt.Errorf("choose a script from this Crew; call another Crew's shared function instead")
		}
		definition, err := readCrewScript(ctx, binding.Root)
		if err != nil {
			return result, err
		}
		if definition.ID != binding.ID {
			return result, fmt.Errorf("saved script identity is invalid")
		}
		sourceRoot := path.Join(binding.Root, "code")
		result = workflow.CrewScriptConfiguration{Definition: *definition, Root: sourceRoot, Files: map[string]string{}}
		files, err := listWorkspaceFilesRecursive(ctx, sourceRoot)
		if err != nil {
			return result, err
		}
		for _, file := range files {
			relative := strings.TrimPrefix(file, sourceRoot+"/")
			if !workflow.CrewScriptBundleFile(relative) {
				continue
			}
			content, found, err := readFileFromWorkspace(ctx, path.Join(sourceRoot, relative))
			if err != nil {
				return result, err
			}
			if found {
				result.Files[relative] = content
			}
		}
		if result.Files["main.py"] == "" {
			return result, fmt.Errorf("save main.py before running this script")
		}
		return result, nil
	}
	return result, fmt.Errorf("saved script is unavailable; choose a script from this Crew")
}

func saveCrewScriptInputs(ctx context.Context, root string, inputs *VariablesManifest) error {
	definition, err := readCrewScript(ctx, root)
	if err != nil {
		return err
	}
	if err := workflow.ValidateWorkflowInputDefinitions(inputs); err != nil {
		return err
	}
	if err := validateWorkflowInputReferences(ctx, root, inputs); err != nil {
		return err
	}
	definition.Inputs = inputs.Variables
	definition.InputPresets = inputs.Groups
	if binding, err := findCrewExecutable(root); err != nil {
		return err
	} else if binding != nil {
		if _, err := crewScriptConsumers(ctx, binding, definition); err != nil {
			return err
		}
	}
	encoded, err := json.MarshalIndent(definition, "", "  ")
	if err != nil {
		return err
	}
	binding, err := findCrewExecutable(root)
	if err != nil || binding == nil {
		return fmt.Errorf("saved script is unavailable")
	}
	if err := writeFileToWorkspace(ctx, path.Join(binding.Root, "code", "script.json"), string(encoded)); err != nil {
		return err
	}
	invalidateWorkflowManifestIndex()
	return nil
}

func writeCrewScriptFile(ctx context.Context, binding *crewExecutableBinding, file, content string) error {
	if path.Base(file) != file || file != "script.json" && !strings.HasSuffix(file, ".py") {
		return fmt.Errorf("choose script.json or a Python file from this script")
	}
	if file == "script.json" {
		definition, err := workflow.ReadCrewScriptDefinition(ctx, binding.Root, func(context.Context, string) (string, error) { return content, nil })
		if err != nil {
			return err
		}
		if definition.ID != binding.ID {
			return fmt.Errorf("keep the saved script identity")
		}
		if _, err := crewScriptCapabilities(ctx, binding.CrewRoot, definition); err != nil {
			return err
		}
		if _, err := crewScriptConsumers(ctx, binding, definition); err != nil {
			return err
		}
		if err := workflow.ValidateWorkflowInputReferences(ctx, binding.Root, definition.InputDefinitions()); err != nil {
			return err
		}
	}
	if err := writeFileToWorkspace(ctx, path.Join(binding.Root, "code", file), content); err != nil {
		return err
	}
	invalidateWorkflowManifestIndex()
	return nil
}

// Source authoring uses the Crew's live Builder identity. Runtime steps and
// Run people receive no write authority from having access to execute a script.
func (api *StreamingAPI) handleCrewScriptFile(w http.ResponseWriter, r *http.Request) {
	binding, err := findCrewExecutable(r.URL.Query().Get("workspace_path"))
	if err != nil || binding == nil || binding.Kind != "script" {
		http.Error(w, "Choose a saved Crew script.", 404)
		return
	}
	claims := GetUserFromContext(r.Context())
	if claims == nil || userAccessForClaims(claims).Disabled || !userAccessForClaims(claims).CanEdit || !userAllowedProduct(claims, "work") || !crewProjectOwnedByCaller(claims.UserID, binding.CrewRoot) {
		http.Error(w, "Only this Crew's Builder can edit its scripts. Run it through chat or a shared function instead.", 403)
		return
	}
	if claims.AccessToken != nil && !api.externalCrewReferenceAllowed(r.Context(), claims, binding.CrewRoot, true) {
		http.Error(w, "This connection cannot edit this Crew's source. Ask in its chat instead.", 403)
		return
	}
	if r.Method != http.MethodGet && refuseCrewOwnerOnline(w, r, crewProjectIDOfRoot(binding.CrewRoot), claims.UserID) {
		return
	}
	file := r.URL.Query().Get("file")
	if path.Base(file) != file || file != "script.json" && !strings.HasSuffix(file, ".py") {
		http.Error(w, "Choose script.json or a Python file.", 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		content, found, err := readFileFromWorkspace(r.Context(), path.Join(binding.Root, "code", file))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{"content": content, "exists": found}})
		return
	}
	var input struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 5<<20)).Decode(&input); err != nil {
		http.Error(w, "Provide the file content.", 400)
		return
	}
	if err := writeCrewScriptFile(r.Context(), binding, file, input.Content); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

type crewScriptUsage struct {
	Workflow string `json:"workflow"`
	Step     string `json:"step"`
}

func crewScriptConsumers(ctx context.Context, script *crewExecutableBinding, next *workflow.CrewScriptDefinition) ([]crewScriptUsage, error) {
	records, err := readCrewExecutables()
	if err != nil {
		return nil, err
	}
	var previous *workflow.CrewScriptDefinition
	if next != nil {
		previous, err = readCrewScript(ctx, script.Root)
		if err != nil {
			return nil, err
		}
	}
	var result []crewScriptUsage
	var affected []string
	for _, binding := range records {
		if binding.Kind != "workflow" || canonicalCrewWorkspaceRoot(binding.CrewRoot) != canonicalCrewWorkspaceRoot(script.CrewRoot) {
			continue
		}
		plan, err := readPlanFromWorkspace(ctx, binding.Root)
		if err != nil {
			continue
		}
		for _, step := range workflow.CrewScriptReferencingSteps(plan, script.ID) {
			label := binding.ID
			if raw, found, _ := readFileFromWorkspace(ctx, binding.Root+"/workflow.json"); found {
				var header struct {
					Label string `json:"label"`
				}
				if json.Unmarshal([]byte(raw), &header) == nil && header.Label != "" {
					label = header.Label
				}
			}
			result = append(result, crewScriptUsage{Workflow: label, Step: step.Title})
			if next == nil {
				continue
			}
			before := map[string]workflow.Variable{}
			after := map[string]workflow.Variable{}
			for _, input := range previous.Inputs {
				before[input.Name] = input
			}
			for _, input := range next.Inputs {
				after[input.Name] = input
			}
			broken := false
			for name := range step.ScriptInputs {
				input, ok := after[name]
				old, was := before[name]
				if !ok || was && (workflow.WorkflowInputType(input) != workflow.WorkflowInputType(old) || !reflect.DeepEqual(input.Schema, old.Schema)) {
					broken = true
				}
			}
			for _, input := range next.Inputs {
				if input.Required && input.Value == "" {
					if _, mapped := step.ScriptInputs[input.Name]; !mapped {
						broken = true
					}
				}
			}
			if broken {
				affected = append(affected, label+" / "+step.Title)
			}
		}
	}
	if len(affected) > 0 {
		return result, fmt.Errorf("these input changes affect workflows. Used by: %s. Update their script input mappings first", strings.Join(affected, ", "))
	}
	return result, nil
}

// PLAT-746-265: a timer runs saved Python directly, just like a function/webhook.
// It must not ask a Builder model to launch the script or do post-run AI work.
func (s *SchedulerService) executeCrewScriptSchedule(ctx context.Context, sctx *ScheduleContext, runID string) (string, string, error) {
	folder := sctx.ScheduledRunFolder
	if folder == "" {
		return "", "", fmt.Errorf("this script schedule has no assigned run folder")
	}
	sessionID := s.newScheduleSessionID(sctx)
	invocation := &workflow.ScheduleInvocation{RunID: runID, ScheduleID: sctx.Schedule.ID, RunFolder: folder, TriggerSource: sctx.TriggerSource, ScheduledFor: sctx.ScheduledFor, AllowParallel: scheduleAllowsParallel(sctx.Schedule)}
	s.api.scheduleInvocations.Store(sessionID, invocation)
	defer s.api.scheduleInvocations.Delete(sessionID)
	s.updateRuntimeState(scheduleRuntimeKey(sctx), func(state *ScheduleRuntimeState) { state.LastSessionID = sessionID })
	if err := UpdateScheduleRun(ctx, sctx.WorkspacePath, runID, "running", "", nil, folder, sessionID); err != nil {
		return sessionID, folder, err
	}
	req := s.buildWorkshopRequest(ctx, sctx, runID)
	opts, err := configureDirectWebhookRequest(req, sctx, folder)
	if err != nil {
		return sessionID, folder, err
	}
	opts.RunKind = invocation.RunKind()
	opts.ScheduleRunID = invocation.RunID
	opts.ScheduleID = invocation.ScheduleID
	opts.TriggerSource = invocation.TriggerSource
	opts.ScheduledFor = invocation.ScheduledFor
	req["query"] = "Script: " + sctx.Schedule.Name
	s.sessionLogf(sctx, sessionID, "[CREW_SCRIPT] Direct schedule execution run=%s run_as=%s folder=%s", runID, sctx.OwnerUserID, folder)
	err = s.api.startSessionInternal(context.WithValue(ctx, directWebhookExecutionKey{}, opts), req, sessionID, sctx.OwnerUserID, nil)
	sctx.ProducedRunEvidence = err == nil
	return sessionID, folder, err
}
