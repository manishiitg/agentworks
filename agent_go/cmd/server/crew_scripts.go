package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"path"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
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
	manifest.DeclaredSecrets = append([]string{}, definition.Secrets...)
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
func (r *crewWorkflowResources) ResolveCrewScriptScope(ctx context.Context, workspace, id string) (workflow.CrewScriptConfiguration, error) {
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
		sourceRoot := path.Join(binding.Root, "code")
		result = workflow.CrewScriptConfiguration{Definition: workflow.CrewScriptDefinition{ID: binding.ID}, Root: sourceRoot}
		// Match the existing account executor's scope, including its established
		// shared default. Tool admission must neither narrow nor widen its grants.
		if google := services.GetGmailService(); google != nil {
			if connection, err := google.ConnectionForScope("", services.GmailUseScope{Workspace: binding.Root, UserID: r.userID}); err == nil && connection.Enabled {
				result.ToolSelections = []string{"human_tools:integrations.google"}
			}
		}
		log.Printf("[SCRIPT_APP_SCOPE] script=%s workspace=%s owner=%s google_available=%t", binding.ID, binding.Root, r.userID, len(result.ToolSelections) > 0)

		return result, nil
	}
	return result, fmt.Errorf("saved script is unavailable; choose a script from this Crew")
}

// Fresh launches read editable source only after the server resolves its scope.
// Resume uses the same scope checks, then loads its verified captured bundle.
func (r *crewWorkflowResources) ResolveCrewScript(ctx context.Context, workspace, id string) (workflow.CrewScriptConfiguration, error) {
	result, err := r.ResolveCrewScriptScope(ctx, workspace, id)
	if err != nil || result.Definition.ID == "" {
		return result, err
	}
	definition, err := readCrewScript(ctx, path.Dir(result.Root))
	if err != nil {
		return result, err
	}
	if definition.ID != result.Definition.ID {
		return result, fmt.Errorf("saved script identity is invalid")
	}
	result.Definition = *definition
	result.Files = map[string][]byte{}

	files, err := listWorkspaceFilesRecursive(ctx, result.Root)
	if err != nil {
		return result, err
	}
	for _, file := range files {
		relative := strings.TrimPrefix(file, result.Root+"/")
		if !workflow.CrewScriptBundleFile(relative) {
			continue
		}
		content, err := fs.ReadFile(workflowfiles.WorkflowFS{Docs: workflow.GetPromptDocsRoot(), Root: path.Dir(result.Root)}, path.Join("code", relative))
		if err != nil {
			return result, err
		}
		result.Files[relative] = content
	}
	if len(result.Files["main.py"]) == 0 {
		return result, fmt.Errorf("save main.py before running this script")
	}
	return result, nil
}

func saveCrewScriptInputs(ctx context.Context, root string, inputs *VariablesManifest, edits ...*scriptEntryInputEdits) error {
	definition, err := readCrewScript(ctx, root)
	if err != nil {
		return err
	}
	definition.Inputs, definition.InputPresets = inputs.Variables, inputs.Groups
	binding, err := findCrewExecutable(root)
	if err != nil || binding == nil {
		return fmt.Errorf("saved script is unavailable")
	}
	var entries *scriptEntryInputEdits
	if len(edits) > 0 {
		entries = edits[0]
	}
	return saveCrewScriptDefinition(ctx, binding, definition, entries)
}

func crewScriptEditableFile(file string) bool {
	return workflow.CrewScriptBundleFile(file) && (file == "script.json" || file == "requirements.txt" || strings.HasSuffix(file, ".py"))
}

func writeCrewScriptFile(ctx context.Context, binding *crewExecutableBinding, file, content string, edits ...*scriptEntryInputEdits) error {
	if !crewScriptEditableFile(file) {
		return fmt.Errorf("choose script.json, requirements.txt or a Python file from this script")
	}
	if file == "requirements.txt" {
		if len(content) > 64<<10 {
			return fmt.Errorf("requirements.txt must be no larger than 64 KB")
		}
		if !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
			return fmt.Errorf("requirements.txt must be UTF-8 text listing the needed packages.")
		}
		// Match the installer before saving: one file is the complete package
		// contract, including continued lines and an optional UTF-8 BOM.
		text := strings.TrimPrefix(content, "\ufeff")
		text = strings.ReplaceAll(strings.ReplaceAll(text, "\\\r\n", ""), "\\\n", "")
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "-") {
				return fmt.Errorf("Put the package pins directly in requirements.txt; included files, constraints and installer option lines are not supported.")
			}
		}
	}
	if file != "script.json" && len(edits) > 0 && edits[0] != nil {
		return fmt.Errorf("save entry_mappings with script.json or Inputs")
	}
	if file == "script.json" {
		definition, err := workflow.ReadCrewScriptDefinition(ctx, binding.Root, func(context.Context, string) (string, error) { return content, nil })
		if err != nil {
			return err
		}
		var entries *scriptEntryInputEdits
		if len(edits) > 0 {
			entries = edits[0]
		}
		return saveCrewScriptDefinition(ctx, binding, definition, entries)
	}
	if err := writeFileToWorkspace(ctx, path.Join(binding.Root, "code", file), content); err != nil {
		return err
	}
	invalidateWorkflowManifestIndex()
	return nil
}

// Source authoring uses the Crew's live Builder identity. Runtime steps and
// Run people receive no write authority from having access to execute a script.
func (api *StreamingAPI) crewScriptAuthoringBinding(w http.ResponseWriter, r *http.Request) *crewExecutableBinding {
	binding, err := findCrewExecutable(r.URL.Query().Get("workspace_path"))
	if err != nil || binding == nil || binding.Kind != "script" {
		http.Error(w, "Choose a saved Crew script.", 404)
		return nil
	}
	claims := GetUserFromContext(r.Context())
	if claims == nil || userAccessForClaims(claims).Disabled || !userAccessForClaims(claims).CanEdit || !userAllowedProduct(claims, "work") || !crewProjectOwnedByCaller(claims.UserID, binding.CrewRoot) {
		http.Error(w, "Only this Crew's Builder can edit its scripts. Run it through chat or a shared function instead.", 403)
		return nil
	}
	if claims.AccessToken != nil && !api.externalCrewReferenceAllowed(r.Context(), claims, binding.CrewRoot, true) {
		http.Error(w, "This connection cannot edit this Crew's source. Ask in its chat instead.", 403)
		return nil
	}
	if r.Method != http.MethodGet && refuseCrewOwnerOnline(w, r, crewProjectIDOfRoot(binding.CrewRoot), claims.UserID) {
		return nil
	}
	return binding
}

func (api *StreamingAPI) handleCrewScriptFile(w http.ResponseWriter, r *http.Request) {
	binding := api.crewScriptAuthoringBinding(w, r)
	if binding == nil {
		return
	}
	file := r.URL.Query().Get("file")
	if !crewScriptEditableFile(file) {
		http.Error(w, "Choose script.json, requirements.txt or a Python file.", 400)
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
		Content       string                 `json:"content"`
		EntryMappings *scriptEntryInputEdits `json:"entry_mappings,omitempty"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 5<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		http.Error(w, "Provide the file content.", 400)
		return
	}
	if err := writeCrewScriptFile(r.Context(), binding, file, input.Content, input.EntryMappings); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	result := crewScriptSaveNotes(r.Context(), binding, file)
	if result == nil {
		result = map[string]interface{}{}
	}
	result["success"] = true
	json.NewEncoder(w).Encode(result)
}

// The Plan view reads exactly the saved code under the same authoring access
// check as the file editor. AST inspection never imports that authored code.
func (api *StreamingAPI) handleCrewScriptGraph(w http.ResponseWriter, r *http.Request) {
	binding := api.crewScriptAuthoringBinding(w, r)
	if binding == nil {
		return
	}
	owner, ok := crewProjectOwnerID(binding.CrewRoot)
	if !ok {
		http.Error(w, "This Crew is unavailable.", 404)
		return
	}
	resource := &crewWorkflowResources{userID: owner}
	source, err := resource.ResolveCrewScript(r.Context(), binding.Root, "")
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	files := map[string]string{}
	for name, content := range source.Files {
		if strings.HasSuffix(name, ".py") {
			files[name] = string(content)
		}
	}
	graph, err := inspectPythonAutomationSource(r.Context(), binding.Root, files)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	// The server resolves each agent's model, so the page only prints it.
	if manifest, _, err := crewScriptManifest(r.Context(), binding); err == nil {
		if withModels, err := addOutlineModels(graph, manifest.Capabilities.LLMConfig); err == nil {
			graph = withModels
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(graph)
}

// crewScriptStepCount is the number of steps the Workflow page's Steps card counts: the outline's nodes that are
// not containers. The Crew panel prints the same number from here. ok is false when there is no readable outline.
func crewScriptStepCount(ctx context.Context, binding crewExecutableBinding) (int, bool) {
	return cachedCrewStepCount(ctx, binding.Root)
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
	s.updateRuntimeState(scheduleRuntimeKey(sctx), func(state *ScheduleRuntimeState) { state.LastSessionID = sessionID })
	if err := UpdateScheduleRun(ctx, sctx.WorkspacePath, runID, "running", "", nil, folder, sessionID); err != nil {
		return sessionID, folder, err
	}
	err := s.startCrewScriptInvocation(ctx, sctx, runID, sessionID, folder, nil)
	sctx.ProducedRunEvidence = err == nil
	return sessionID, folder, err
}

// Chat timers and the remaining old timer adapter share this direct saved
// Python starter. The caller owns admission, chat receipts and the run ledger.
func (s *SchedulerService) startCrewScriptInvocation(ctx context.Context, sctx *ScheduleContext, runID, sessionID, folder string, costJob *productScheduleJob) error {
	invocation := &workflow.ScheduleInvocation{RunID: runID, ScheduleID: sctx.Schedule.ID, RunFolder: folder, TriggerSource: sctx.TriggerSource, ScheduledFor: sctx.ScheduledFor, AllowParallel: costJob != nil || scheduleAllowsParallel(sctx.Schedule)}
	s.api.scheduleInvocations.Store(sessionID, invocation)
	defer s.api.scheduleInvocations.Delete(sessionID)
	req := s.buildWorkshopRequest(ctx, sctx, runID)
	opts, err := configureDirectWebhookRequest(req, sctx, folder)
	if err != nil {
		return err
	}
	opts.RunKind = invocation.RunKind()
	opts.ScheduleRunID = invocation.RunID
	opts.ScheduleID = invocation.ScheduleID
	opts.TriggerSource = invocation.TriggerSource
	opts.ScheduledFor = invocation.ScheduledFor
	req["query"] = "Script: " + sctx.Schedule.Name
	if costJob != nil {
		applyAutomationCostSource(req, *costJob, runID)
	}
	s.sessionLogf(sctx, sessionID, "[CREW_SCRIPT] Direct schedule execution run=%s run_as=%s folder=%s", runID, sctx.OwnerUserID, folder)
	return s.api.startSessionInternal(context.WithValue(ctx, directWebhookExecutionKey{}, opts), req, sessionID, sctx.OwnerUserID, nil)
}
