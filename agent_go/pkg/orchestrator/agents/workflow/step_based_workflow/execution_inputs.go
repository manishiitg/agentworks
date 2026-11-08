package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var permanentRunFolder = regexp.MustCompile(`^run-[a-z0-9-]+$`)

func IsPermanentRunFolder(folder string) bool { return permanentRunFolder.MatchString(folder) }

// Run identity is one directory component, independent of any input preset.
func ValidateWorkflowRunFolder(folder string) error {
	if folder == "" || filepath.IsAbs(folder) || filepath.Clean(folder) != folder || strings.ContainsAny(folder, "/\\\x00") || strings.HasPrefix(folder, ".") {
		return fmt.Errorf("invalid workflow run folder; choose an existing run from this workflow")
	}
	return nil
}

const RetiredWorkflowGroupsMessage = "group selection is retired; use optional input_preset and inputs for one run"

// A continuation must already contain its immutable input record. Reading it
// also proves the selected run exists in this workflow, before any work starts.
func (hcpo *StepBasedWorkflowOrchestrator) readExistingRunInputs(ctx context.Context, folder string) (map[string]string, error) {
	if err := ValidateWorkflowRunFolder(folder); err != nil {
		return nil, err
	}
	raw, err := hcpo.ReadWorkspaceFile(ctx, filepath.Join(hcpo.GetWorkspacePath(), "runs", folder, "inputs.json"))
	if err != nil {
		return nil, fmt.Errorf("run is unavailable; choose an existing run or omit run_folder to start a new one: %w", err)
	}
	return hcpo.decodeRunInputSnapshot(raw)
}

// Saved input labels are data, never directory names or execution identities.
func ValidateInputSetName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("saved inputs need a name")
	}
	return nil
}

// ResolveWorkflowInputs is the one precedence rule for an invocation. Presets
// only fill values; they never select routes, create batches or supply authority.
func ResolveWorkflowInputs(manifest *VariablesManifest, preset string, overrides map[string]string) (map[string]string, error) {
	values := map[string]string{}
	declared := map[string]Variable{}
	if manifest != nil {
		for _, variable := range manifest.Variables {
			declared[variable.Name] = variable
			// An unset typed default is absence, not an explicit empty value.
			// Preserve that distinction in saved inputs and direct script calls.
			if variable.Value != "" || WorkflowInputType(variable) == "string" {
				values[variable.Name] = variable.Value
			}
		}
	}
	if preset != "" {
		found := false
		if manifest != nil {
			for _, saved := range manifest.Groups {
				if saved.Name != preset {
					continue
				}
				if found {
					return nil, fmt.Errorf("duplicate saved inputs %q; give each preset a unique name", preset)
				}
				found = true
				for name, value := range saved.Values {
					if _, found := declared[name]; !found {
						return nil, fmt.Errorf("saved inputs contain undeclared input %q; add it to the workflow inputs first", name)
					}
					if _, err := WorkflowInputValue(declared[name], value); err != nil {
						return nil, err
					}
					values[name] = value
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("saved inputs %q not found; choose an existing input_preset or omit it to use defaults", preset)
		}
	}
	for name, value := range overrides {
		if _, found := declared[name]; !found {
			return nil, fmt.Errorf("unknown input %q; add it to the workflow inputs first", name)
		}
		values[name] = value
	}
	for name, value := range values {
		_, explicit := overrides[name]
		if value != "" || explicit {
			if _, err := WorkflowInputValue(declared[name], value); err != nil {
				return nil, err
			}
		}
	}
	return values, nil
}

// ResolveSelectedInputPreset resolves one invocation from the workshop selection.
// Omission uses defaults; saved presets never become an implicit batch.
func ResolveSelectedInputPreset(manifest *VariablesManifest, selected []string) (VariableGroup, error) {
	if selected != nil && len(selected) == 0 {
		return VariableGroup{}, fmt.Errorf("no saved inputs selected; omit the selection to use defaults")
	}
	if len(selected) > 1 {
		return VariableGroup{}, fmt.Errorf("choose one saved input preset per run; start separate runs for several presets")
	}
	preset := ""
	if len(selected) == 1 {
		preset = selected[0]
	}
	values, err := ResolveWorkflowInputs(manifest, preset, nil)
	if err != nil {
		return VariableGroup{}, err
	}
	return VariableGroup{Name: preset, Values: values}, nil
}

func (hcpo *StepBasedWorkflowOrchestrator) resolveWorkshopInputGroup(ctx context.Context, requested string, selection []string) (string, error) {
	_, manifest, err := NewVariableManager(hcpo.BaseOrchestrator).checkExistingVariables(ctx, "variables/variables.json")
	if err != nil {
		return "", err
	}
	if hcpo.directCrewScript != nil {
		manifest = hcpo.directCrewScript.InputDefinitions()
	}
	hcpo.variablesManifest = manifest
	if requested != "" {
		selection = []string{requested}
	}
	resolved, err := ResolveSelectedInputPreset(manifest, selection)
	if err != nil {
		return "", err
	}
	return resolved.Name, nil
}

type workflowRunInputs struct {
	Preset string            `json:"input_preset,omitempty"`
	Values map[string]string `json:"inputs"`
}

// Existing runs retain their resolved values when a preset is edited. Store
// the snapshot alongside progress, logs and outputs, not in the definition.
func (hcpo *StepBasedWorkflowOrchestrator) resolveRunInputs(ctx context.Context, folder, preset string, overrides map[string]string) (map[string]string, error) {
	path := filepath.Join(hcpo.GetWorkspacePath(), "runs", folder, "inputs.json")
	raw, err := hcpo.ReadWorkspaceFile(ctx, path)
	if err == nil {
		return hcpo.decodeRunInputSnapshot(raw)
	}
	text := strings.ToLower(err.Error())
	if !strings.Contains(text, "not found") && !strings.Contains(text, "no such file") && !strings.Contains(text, "does not exist") {
		return nil, fmt.Errorf("read run inputs: %w", err)
	}
	if err := hcpo.validateInputSecretBindings(overrides); err != nil {
		return nil, err
	}

	values := cloneWorkflowStringMap(overrides)
	if values == nil {
		values = map[string]string{}
	}
	if err := hcpo.createRunFolderStructure(ctx, filepath.Dir(path)); err != nil {
		return nil, err
	}
	var snapshot interface{} = workflowRunInputs{Preset: preset, Values: values}
	if hcpo.directCrewScript != nil {
		resolved, err := ResolveWorkflowInputs(hcpo.directCrewScript.InputDefinitions(), "", values)
		if err != nil {
			return nil, err
		}
		values = resolved
		if err := hcpo.validateInputSecretBindings(values); err != nil {
			return nil, err
		}
		snapshot, err = typedWorkflowInputs(hcpo.directCrewScript.InputDefinitions(), resolved)
		if err != nil {
			return nil, err
		}
	}
	rawBytes, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, err
	}
	if hcpo.directCrewScript != nil {
		// Typed inputs stay a plain value map. Keep their selected label in
		// the run record so diagnostics/rerun need not guess it from values.
		// Existing input snapshots return above and never relabel a resumed run.
		if err := hcpo.upsertRunMetadata(ctx, folder, func(meta map[string]interface{}) {
			meta["input_preset"] = preset
		}); err != nil {
			return nil, fmt.Errorf("save run input preset: %w", err)
		}
	}
	if err := hcpo.WriteWorkspaceFile(ctx, path, string(rawBytes)); err != nil {
		return nil, fmt.Errorf("save run inputs: %w", err)
	}
	return values, nil
}

// A present null or empty preset clears a saved choice; omission preserves it.
func parseSavedInputArguments(args map[string]interface{}) (*string, map[string]string, error) {
	var preset *string
	if raw, present := args["input_preset"]; present {
		value := ""
		if raw != nil {
			var ok bool
			value, ok = raw.(string)
			if !ok {
				return nil, nil, fmt.Errorf("input_preset must be a string or null")
			}
		}
		preset = &value
	}
	inputs, err := parseWorkflowStepStringMap(args, "inputs")
	return preset, inputs, err
}

func ValidateWorkflowInputDefinitions(manifest *VariablesManifest) error {
	if manifest == nil {
		return nil
	}
	names := map[string]bool{}
	for _, variable := range manifest.Variables {
		if strings.TrimSpace(variable.Name) == "" || names[variable.Name] {
			return fmt.Errorf("inputs need unique non-empty names")
		}
		if _, err := WorkflowInputProperty(variable); err != nil {
			return err
		}
		names[variable.Name] = true
	}
	if _, err := ResolveWorkflowInputs(manifest, "", nil); err != nil {
		return err
	}
	saved := map[string]bool{}
	for _, preset := range manifest.Groups {
		if err := ValidateInputSetName(preset.Name); err != nil {
			return err
		}
		if saved[preset.Name] {
			return fmt.Errorf("saved inputs need unique names")
		}
		saved[preset.Name] = true
		if _, err := ResolveWorkflowInputs(manifest, preset.Name, nil); err != nil {
			return err
		}
	}
	return nil
}

// Inputs are ordinary data. Their names cannot impersonate a selected Vault binding.
func (hcpo *StepBasedWorkflowOrchestrator) validateInputSecretBindings(values map[string]string) error {
	for _, secret := range hcpo.GetSecrets() {
		for name, value := range values {
			if secret.Value != "" && value == secret.Value {
				return fmt.Errorf("input %q contains a Vault value; use the configured secret binding instead", name)
			}
		}
		for _, name := range []string{secret.Name, "SECRET_" + secret.Name} {
			if _, exists := values[name]; exists {
				return fmt.Errorf("input %q clashes with a selected Vault secret; rename the input or use its Vault binding", name)
			}
		}
	}
	return nil
}

func (hcpo *StepBasedWorkflowOrchestrator) decodeRunInputSnapshot(raw string) (map[string]string, error) {

	if hcpo.directCrewScript != nil {
		var typed map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &typed); err != nil {
			return nil, fmt.Errorf("read saved script inputs: %w", err)
		}
		values := map[string]string{}
		for _, input := range hcpo.directCrewScript.Inputs {
			value, exists := typed[input.Name]
			if !exists {
				continue
			}
			if WorkflowInputType(input) == "string" {
				var decoded interface{}
				if err := json.Unmarshal(value, &decoded); err != nil {
					return nil, err
				}
				text, ok := decoded.(string)
				if !ok {
					return nil, fmt.Errorf("saved input %q must be text", input.Name)
				}
				values[input.Name] = text
			} else {
				values[input.Name] = string(value)
			}
			delete(typed, input.Name)
		}
		if len(typed) > 0 {
			return nil, fmt.Errorf("saved script inputs contain undeclared names")
		}
		if err := hcpo.validateInputSecretBindings(values); err != nil {
			return nil, err
		}
		return ResolveWorkflowInputs(hcpo.directCrewScript.InputDefinitions(), "", values)
	}

	var recorded workflowRunInputs
	if err := json.Unmarshal([]byte(raw), &recorded); err != nil {
		return nil, fmt.Errorf("read saved run inputs: %w", err)
	}
	if recorded.Values == nil {
		return nil, fmt.Errorf("saved run inputs are missing")
	}
	if err := hcpo.validateInputSecretBindings(recorded.Values); err != nil {
		return nil, err
	}
	return recorded.Values, nil
}
