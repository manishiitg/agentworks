package server

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowkb"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Crew executable identities live outside editable workspace files. The shared
// Crew root resolver routes definitions/code separately from runtime state.
// PLAT-746-265: a display name or manifest owner is never an execution authority.
type crewExecutableBinding = crewroot.ExecutableBinding

var crewExecutablesMu sync.Mutex

var errCrewWorkflowExists = errors.New("this Crew already has a workflow; edit its existing steps or add a route")

func crewExecutablesPath() (string, error) {
	root, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "ownership", "crew-executables"), nil
}

func crewExecutableWorkspace(raw string) string {
	clean := filepath.ToSlash(filepath.Clean(strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")))
	if docs := strings.TrimSpace(os.Getenv("WORKSPACE_DOCS_PATH")); docs != "" && filepath.IsAbs(clean) {
		if rel, err := filepath.Rel(docs, clean); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			clean = filepath.ToSlash(rel)
		}
	}
	return path.Clean(strings.Trim(clean, "/"))
}

func crewExecutableKey(raw string) string { return workflowOwnerKey(crewExecutableWorkspace(raw)) }

func crewExecutableFile(root string) (string, error) {
	dir, err := crewExecutablesPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%x.json", sha256.Sum256([]byte(root)))), nil
}

func readCrewExecutables() (map[string]crewExecutableBinding, error) {
	dir, err := crewExecutablesPath()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	result := map[string]crewExecutableBinding{}
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		var binding crewExecutableBinding
		if err == nil {
			err = json.Unmarshal(raw, &binding)
		}
		if err != nil {
			log.Printf("[CREW_EXECUTABLE] unavailable record=%s reason=%v", entry.Name(), err)
			continue
		}
		expected, _ := crewExecutableFile(binding.Root)
		if binding.Root != crewExecutableKey(binding.Root) || filepath.Base(expected) != entry.Name() {
			continue
		}
		if binding.Kind != "workflow" && binding.Kind != "script" {
			continue
		}
		result[binding.Root] = binding
	}
	return result, nil
}

func findCrewExecutable(rawRoot string) (*crewExecutableBinding, error) {
	workspace := crewExecutableWorkspace(rawRoot)

	root := crewExecutableKey(workspace)
	if root == "" {
		return nil, nil
	}
	file, err := crewExecutableFile(root)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read executable binding: %w", err)
	}
	var binding crewExecutableBinding
	if err := json.Unmarshal(data, &binding); err != nil {
		return nil, fmt.Errorf("read executable binding: %w", err)
	}
	if binding.Root != root || binding.ID == "" || binding.CrewID == "" || (binding.Kind != "workflow" && binding.Kind != "script") {
		return nil, fmt.Errorf("invalid executable binding")
	}
	owner, found := crewProjectOwnerID(binding.CrewRoot)
	if !found || owner == "" {
		return nil, fmt.Errorf("owning Crew is unavailable")
	}
	return &binding, nil
}

// Old workflows may have a folder name distinct from their stable manifest ID.
// Resolve through the private registry; never synthesize a filesystem path from
// an ID or accept an editable manifest as ownership evidence.
func findCrewExecutableByID(id string) (*crewExecutableBinding, error) {
	bindings, err := readCrewExecutables()
	if err != nil {
		return nil, err
	}
	var result *crewExecutableBinding
	for _, binding := range bindings {
		if binding.ID != id {
			continue
		}
		if result != nil {
			return nil, fmt.Errorf("workflow identity is registered more than once")
		}
		copy := binding
		result = &copy
	}
	return result, nil
}

func registerCrewExecutable(binding crewExecutableBinding) error {
	crewExecutablesMu.Lock()
	defer crewExecutablesMu.Unlock()
	binding.Root = crewExecutableKey(binding.Root)
	if binding.RuntimeRoot == "" {
		binding.RuntimeRoot = "automations/" + binding.ID
	}
	if binding.Root == "" {
		return fmt.Errorf("invalid executable root")
	}
	file, err := crewExecutableFile(binding.Root)
	if err != nil {
		return err
	}
	if _, err := os.Stat(file); err == nil {
		return fmt.Errorf("executable already registered")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if binding.Kind == "workflow" {
		records, err := readCrewExecutables()
		if err != nil {
			return err
		}
		for _, existing := range records {
			if existing.CrewID == binding.CrewID && existing.Kind == "workflow" {
				return fmt.Errorf("%w (%s)", errCrewWorkflowExists, existing.Root)
			}
		}
	}
	data, err := json.MarshalIndent(binding, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	return writeRegistryFileAtomically(file, data)
}

// Removing a Crew also removes its executable identities. Use the stable
// server-owned Crew ID, so a stale folder name cannot retain an orphan binding.
func removeCrewExecutableBindings(crewID string) error {
	if strings.TrimSpace(crewID) == "" {
		return fmt.Errorf("Crew identity is required for executable cleanup")
	}
	crewExecutablesMu.Lock()
	defer crewExecutablesMu.Unlock()
	bindings, err := readCrewExecutables()
	if err != nil {
		return err
	}
	for root, binding := range bindings {
		if binding.CrewID != crewID {
			continue
		}
		file, err := crewExecutableFile(root)
		if err != nil {
			return err
		}
		if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// All actions use the existing Crew resolver, including its co-owner and token
// checks. A Run person cannot author executables through this Builder endpoint.
func (api *StreamingAPI) handleCrewExecutables(w http.ResponseWriter, r *http.Request) {
	if api.projectActions == nil || api.projectActions.registry == nil {
		http.Error(w, "Crew services are unavailable", http.StatusServiceUnavailable)
		return
	}
	caller := GetUserIDFromContext(r.Context())
	profile, err := api.projectActions.registry.Resolve("work", 0, caller)
	if err != nil {
		http.Error(w, "Crew is unavailable", http.StatusNotFound)
		return
	}
	project, err := resolveCrewProjectBinding(r.Context(), caller, profile, r.URL.Query().Get("crew_id"), "")
	if err != nil || !project.OwnedByCaller {
		http.Error(w, "Only this Crew's owners can manage its workflows.", http.StatusForbidden)
		return
	}
	root := project.Binding.WorkspacePath
	if r.Method == http.MethodGet {
		records, err := readCrewExecutables()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		type entry struct {
			crewExecutableBinding
			Manifest *WorkflowManifest `json:"manifest"`
			UsedBy   []crewScriptUsage `json:"used_by,omitempty"`
			// What a person can change before a run, and the saved settings that fill it in (a Python workflow's script.json).
			Inputs       []workflow.Variable      `json:"inputs,omitempty"`
			InputPresets []workflow.VariableGroup `json:"input_presets,omitempty"`
			// The other files a Python workflow's main.py uses (helpers, prompts), relative to its code folder.
			Files []string `json:"files,omitempty"`
		}
		entries := []entry{}
		// Python that a workflow's own steps run: listed beside the saved Crew scripts, each labelled by where it is used.
		type stepScript struct {
			WorkflowID string `json:"workflow_id"`
			Workflow   string `json:"workflow"`
			StepID     string `json:"step_id"`
			Step       string `json:"step"`
		}
		stepScripts := []stepScript{}
		for _, binding := range records {
			if binding.CrewRoot != root {
				continue
			}
			manifest, found, err := ReadWorkflowManifest(r.Context(), binding.Root)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if found {
				var consumers []crewScriptUsage
				var inputs []workflow.Variable
				var presets []workflow.VariableGroup
				var files []string
				if binding.Kind == "script" {
					codeRoot := path.Join(binding.Root, "code")
					if listed, listErr := listWorkspaceFilesRecursive(r.Context(), codeRoot); listErr == nil {
						for _, name := range listed {
							name = strings.TrimPrefix(strings.TrimPrefix(name, codeRoot), "/")
							if name != "main.py" && name != "script.json" && crewScriptEditableFile(name) {
								files = append(files, name)
							}
						}
					}
					consumers, err = crewScriptConsumers(r.Context(), &binding, nil)
					if err != nil {
						http.Error(w, err.Error(), 500)
						return
					}
					if definition, readErr := readCrewScript(r.Context(), binding.Root); readErr == nil {
						inputs, presets = definition.Inputs, definition.InputPresets
					}
				}
				entries = append(entries, entry{crewExecutableBinding: binding, Manifest: manifest, UsedBy: consumers, Inputs: inputs, InputPresets: presets, Files: files})
				if binding.Kind == "workflow" {
					if plan, planErr := readPlanFromWorkspace(r.Context(), binding.Root); planErr == nil {
						for _, step := range workflow.CrewWorkflowOwnScriptSteps(plan) {
							stepScripts = append(stepScripts, stepScript{WorkflowID: binding.ID, Workflow: manifest.Label, StepID: step.ID, Step: firstNonEmptyTrimmed(step.Title, step.ID)})
						}
					}
				}
			}
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Manifest.Label < entries[j].Manifest.Label })
		w.Header().Set("Content-Type", "application/json")
		agents, err := crewExecutableAgentChoices(r.Context(), caller, project.OwnerID, project.Binding.ResourceID, root)
		if err != nil {
			http.Error(w, "Could not load this Crew's agents", http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"executables": entries, "step_scripts": stepScripts, "agents": agents})
		return
	}
	if refuseCrewOwnerOnline(w, r, crewProjectIDOfRoot(root), caller) {
		return
	}
	if !workflowPermissionInfoForClaims(GetUserFromContext(r.Context())).CanWriteWorkflows {
		http.Error(w, "Your account cannot edit workflows. Ask an administrator to restore edit access.", http.StatusForbidden)
		return
	}
	var input struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		http.Error(w, "Enter a workflow name.", http.StatusBadRequest)
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Kind != "" && input.Kind != "workflow" && input.Kind != "script" {
		http.Error(w, "Create a Python workflow in this project.", http.StatusBadRequest)
		return
	}
	if input.Name == "" || len(input.Name) > 120 {
		http.Error(w, "Enter a workflow name (up to 120 characters).", http.StatusBadRequest)
		return
	}
	// PLAT-746: every new workflow is a Python script; kind "workflow" from an
	// older caller creates one too. Existing plan workflows keep working.
	id := uuid.NewString()
	binding := crewExecutableBinding{ID: id, CrewID: project.Binding.ResourceID, CrewRoot: root, Root: "Workflow/" + id, Kind: "script"}
	if err := registerCrewExecutable(binding); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	registerWorkflowOwner(binding.Root, project.OwnerID)
	if err := initializeCrewScriptSource(r.Context(), binding, input.Name); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if api.scheduler != nil {
		api.scheduler.InvalidateWorkflowManifestCache()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	json.NewEncoder(w).Encode(binding)
}

// PLAT-746-265: KB authorization also reads manifests directly from disk.
// Resolve both consumers and sources through the same trusted parent audience.
func init() { workflowkb.ManifestAuthority = crewExecutableKnowledgeAudience }

func crewExecutableKnowledgeAudience(root string, manifest workflowkb.Manifest) (workflowkb.Manifest, error) {
	binding, err := findCrewExecutable(root)
	if err != nil || binding == nil {
		return manifest, err
	}
	if manifest.ID != binding.ID {
		return manifest, fmt.Errorf("knowledge executable identity does not match its Crew")
	}
	id, ok := projectIdentityOf(binding.CrewRoot)
	if !ok {
		return manifest, fmt.Errorf("knowledge Crew is unavailable")
	}
	record, found := defaultProjectOwners().Lookup(id.Product, id.Folder)
	if !found || record.OwnerID == "" {
		return manifest, fmt.Errorf("knowledge Crew owner is unavailable")
	}
	manifest.CreatedBy = record.OwnerID
	// Keep source-specific writer opt-in; only audience/ownership are inherited.
	if manifest.Access == nil {
		manifest.Access = &struct {
			Owners           []string `json:"owners"`
			Readers          []string `json:"readers"`
			AllowedKBWriters []string `json:"allowed_kb_writers"`
		}{}
	}
	manifest.Access.Owners = append([]string{record.OwnerID}, crewCoOwnerIDs(binding.CrewRoot)...)
	manifest.Access.Readers = nil
	if projectSharingEnabled() {
		manifest.Access.Readers = append([]string{}, record.RunPeople...)
	}
	manifest.Everyone = crewSharedWithOthers(binding.CrewRoot)
	return manifest, nil
}
