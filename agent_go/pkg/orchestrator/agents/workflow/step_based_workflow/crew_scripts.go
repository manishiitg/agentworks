package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// CrewScriptDefinition owns code and input contracts independently of a plan.
// The existing step engine receives a plan projection, never a second saved plan.
type CrewScriptDefinition struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	Inputs           []Variable        `json:"inputs,omitempty"`
	Outputs          []string          `json:"outputs,omitempty"`
	ValidationSchema *ValidationSchema `json:"validation_schema,omitempty"`
}

type ScriptInputBinding struct {
	Value  json.RawMessage `json:"value,omitempty"`
	Input  string          `json:"input,omitempty"`
	Output string          `json:"output,omitempty"`
	Field  string          `json:"field,omitempty"`
}

type CrewScriptConfiguration struct {
	Definition CrewScriptDefinition
	Root       string
	Files      map[string]string
}
type CrewScriptResolver interface {
	ResolveCrewScript(context.Context, string, string) (CrewScriptConfiguration, error)
}

func ReadCrewScriptDefinition(ctx context.Context, root string, read func(context.Context, string) (string, error)) (*CrewScriptDefinition, error) {
	raw, err := read(ctx, filepath.ToSlash(filepath.Join(root, "script.json")))
	if err != nil {
		return nil, err
	}
	var definition CrewScriptDefinition
	if err := json.Unmarshal([]byte(raw), &definition); err != nil {
		return nil, fmt.Errorf("read script definition: %w", err)
	}
	if definition.ID == "" || filepath.Base(definition.ID) != definition.ID || strings.ContainsAny(definition.ID, "/\\.") || strings.TrimSpace(definition.Name) == "" {
		return nil, fmt.Errorf("script needs a stable id and a name")
	}
	if err := ValidateWorkflowInputDefinitions(&VariablesManifest{Variables: definition.Inputs}); err != nil {
		return nil, err
	}
	for _, output := range definition.Outputs {
		if output == "" || filepath.IsAbs(output) || filepath.Clean(output) != output || strings.HasPrefix(output, "..") {
			return nil, fmt.Errorf("script output must be a relative output file")
		}
	}
	return &definition, nil
}

func CrewScriptPlan(definition *CrewScriptDefinition, root string) *PlanningResponse {
	description := definition.Description
	if description == "" {
		description = definition.Name
	}
	step := &ScriptedPlanStep{CommonStepFields: CommonStepFields{ID: definition.ID, Title: definition.Name, Description: description, ContextOutput: FlexibleContextOutput(strings.Join(definition.Outputs, ", ")), ValidationSchema: definition.ValidationSchema}, Script: definition.ID, ScriptOnly: true, crewScript: &CrewScriptConfiguration{Definition: *definition, Root: root}}
	return &PlanningResponse{Steps: []PlanStepInterface{step}}
}

func (hcpo *StepBasedWorkflowOrchestrator) hydrateCrewScripts(ctx context.Context, plan *PlanningResponse) error {
	if plan == nil {
		return nil
	}
	steps := append(append([]PlanStepInterface{}, plan.Steps...), plan.OrphanSteps...)
	for _, info := range collectAllSteps(steps) {
		step, ok := info.Step.(*ScriptedPlanStep)
		if !ok || step.Script == "" || step.crewScript != nil {
			continue
		}
		if len(step.ScriptParameters) > 0 || len(step.ScriptParametersSchema) > 0 {
			return fmt.Errorf("step %q: define input types on the saved script; use script_inputs to map values", step.ID)
		}
		options := hcpo.GetExecutionOptions()
		if options == nil {
			return fmt.Errorf("Crew script resolver is unavailable")
		}
		resources := options.CrewResources
		if resources == nil || resources.Scripts == nil {
			return fmt.Errorf("Crew script resolver is unavailable")
		}
		resolved, err := resources.Scripts.ResolveCrewScript(ctx, hcpo.GetWorkspacePath(), step.Script)
		if err != nil {
			return err
		}
		step.crewScript = &resolved
		step.ScriptOnly = true
		if step.ContextOutput == "" {
			step.ContextOutput = FlexibleContextOutput(strings.Join(resolved.Definition.Outputs, ", "))
		}
		if step.ValidationSchema == nil {
			step.ValidationSchema = resolved.Definition.ValidationSchema
		}
	}
	return nil
}

func (hcpo *StepBasedWorkflowOrchestrator) crewScriptParameters(ctx context.Context, step *ScriptedPlanStep, stepIndex int, stepPath string, allSteps []PlanStepInterface, executionRoot string) (map[string]interface{}, error) {
	valuesByName, err := hcpo.resolveStepInputValues(ctx, step, step.ScriptInputs, stepIndex, stepPath, allSteps, executionRoot, false)
	if err != nil {
		return nil, err
	}
	raw := map[string]string{}
	for name, value := range valuesByName {
		if text, ok := value.(string); ok {
			raw[name] = text
		} else {
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			raw[name] = string(encoded)
		}
	}
	// A direct script invocation uses its own resolved launch inputs.
	if len(step.ScriptInputs) == 0 && hcpo.directCrewScript != nil {
		for name, value := range hcpo.variableValues {
			raw[name] = value
		}
	}
	if delegation, ok := scriptedDelegationFromContext(ctx); ok {
		for name, value := range delegation.Parameters {
			if _, mapped := raw[name]; mapped {
				return nil, fmt.Errorf("step input %q was supplied twice", name)
			}
			if text, ok := value.(string); ok {
				raw[name] = text
			} else {
				encoded, err := json.Marshal(value)
				if err != nil {
					return nil, err
				}
				raw[name] = string(encoded)
			}
		}
	}
	definitions := &VariablesManifest{Variables: step.crewScript.Definition.Inputs}
	values, err := ResolveWorkflowInputs(definitions, "", raw)
	if err != nil {
		return nil, err
	}
	if err := ValidateWorkflowLaunchInputs(definitions, values); err != nil {
		return nil, err
	}
	typed := map[string]interface{}{}
	for _, input := range definitions.Variables {
		value := values[input.Name]
		if value == "" && WorkflowInputType(input) != "string" {
			continue
		}
		var decoded interface{} = value
		if WorkflowInputType(input) != "string" {
			decoder := json.NewDecoder(strings.NewReader(value))
			decoder.UseNumber()
			if err := decoder.Decode(&decoded); err != nil {
				return nil, err
			}
		}
		typed[input.Name] = decoded
	}
	return typed, nil
}

func CrewScriptReferencingSteps(plan *PlanningResponse, id string) []*ScriptedPlanStep {
	var result []*ScriptedPlanStep
	if plan != nil {
		steps := append(append([]PlanStepInterface{}, plan.Steps...), plan.OrphanSteps...)
		for _, info := range collectAllSteps(steps) {
			if step, ok := info.Step.(*ScriptedPlanStep); ok && strings.TrimPrefix(step.Script, "script:") == id {
				result = append(result, step)
			}
		}
	}
	return result
}

// Project the saved script into the runner's existing file interface. These
// definitions are never saved alongside the owner's canonical script.json.
func (hcpo *StepBasedWorkflowOrchestrator) ReadWorkspaceFile(ctx context.Context, file string) (string, error) {
	if hcpo.directCrewScriptConfig != nil {
		relative := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(file)), hcpo.GetWorkspacePath()+"/")
		var value interface{}
		switch relative {
		case "workflow.json":
			value = map[string]interface{}{"code_layout_version": 1}
		case "variables/variables.json":
			value = VariablesManifest{Variables: hcpo.directCrewScript.Inputs}
		case "planning/plan.json":
			value = CrewScriptPlan(hcpo.directCrewScript, hcpo.directCrewScriptConfig.Root)
		}
		if value != nil {
			encoded, err := json.Marshal(value)
			return string(encoded), err
		}
	}
	return hcpo.BaseOrchestrator.ReadWorkspaceFile(ctx, file)
}

func (hcpo *StepBasedWorkflowOrchestrator) WriteWorkspaceFile(ctx context.Context, file, content string) error {
	if hcpo.directCrewScriptConfig != nil {
		relative := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(file)), hcpo.GetWorkspacePath()+"/")
		switch relative {
		case "workflow.json", "variables/variables.json", "planning/plan.json":
			return fmt.Errorf("edit this saved script in its owning Crew; it has one source definition")
		}
	}
	return hcpo.BaseOrchestrator.WriteWorkspaceFile(ctx, file, content)
}

// Bundle members are relative to the trusted script source folder. Include
// helper packages and data files; private dot files and caches are not code.
func CrewScriptBundleFile(file string) bool {
	if file == "" || filepath.IsAbs(file) || filepath.ToSlash(filepath.Clean(file)) != file || strings.Contains(file, "\\") {
		return false
	}
	for _, part := range strings.Split(file, "/") {
		if part == "" || strings.HasPrefix(part, ".") || part == "__pycache__" {
			return false
		}
	}
	return true
}

// Both agent and script steps use the same explicit input-source rules.
func (hcpo *StepBasedWorkflowOrchestrator) resolveStepInputValues(ctx context.Context, step PlanStepInterface, bindings map[string]ScriptInputBinding, stepIndex int, stepPath string, allSteps []PlanStepInterface, executionRoot string, fileReferences bool) (map[string]interface{}, error) {
	if err := validateStepInputBindings(bindings, step.GetContextDependencies()); err != nil {
		return nil, err
	}
	resolved := map[string]interface{}{}
	for name, binding := range bindings {
		sources := 0
		if binding.Value != nil {
			sources++
		}
		if binding.Input != "" {
			sources++
		}
		if binding.Output != "" {
			sources++
		}
		if sources != 1 {
			return nil, fmt.Errorf("step input %q needs one value source", name)
		}
		var value interface{}
		if binding.Value != nil {
			decoder := json.NewDecoder(strings.NewReader(string(binding.Value)))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				return nil, err
			}
		} else if binding.Input != "" {
			text, found := hcpo.variableValues[binding.Input]
			if !found {
				return nil, fmt.Errorf("unknown workflow input %q", binding.Input)
			}
			value = text
			if hcpo.variablesManifest != nil {
				for _, input := range hcpo.variablesManifest.Variables {
					if input.Name == binding.Input && WorkflowInputType(input) != "string" && text != "" {
						decoder := json.NewDecoder(strings.NewReader(text))
						decoder.UseNumber()
						if err := decoder.Decode(&value); err != nil {
							return nil, err
						}
					}
				}
			}
		} else {
			if !slices.Contains(step.GetContextDependencies(), binding.Output) {
				return nil, fmt.Errorf("step input %q must reference a declared context dependency", name)
			}
			paths := hcpo.resolveDependencyPathsWithWorkspace(ctx, []string{binding.Output}, stepIndex, stepPath, allSteps, executionRoot, GetPromptDocsRoot(), hcpo.variableValues)
			if len(paths) != 1 {
				return nil, fmt.Errorf("step input %q output is unavailable", name)
			}
			relative, err := filepath.Rel(filepath.Join(GetPromptDocsRoot(), executionRoot), paths[0])
			if err != nil || strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
				return nil, fmt.Errorf("step input %q must use this run's output", name)
			}
			content, err := hcpo.ReadWorkspaceFile(ctx, strings.TrimPrefix(paths[0], GetPromptDocsRoot()+"/"))
			if err != nil {
				return nil, err
			}
			if fileReferences && binding.Field == "" {
				resolved[name] = map[string]string{"file": paths[0]}
				continue
			}
			decoder := json.NewDecoder(strings.NewReader(content))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				return nil, fmt.Errorf("step input %q expects JSON output", name)
			}
			if binding.Field != "" {
				for _, part := range strings.Split(binding.Field, ".") {
					object, ok := value.(map[string]interface{})
					if !ok {
						return nil, fmt.Errorf("output field %q is unavailable", binding.Field)
					}
					value, ok = object[part]
					if !ok {
						return nil, fmt.Errorf("output field %q is unavailable", binding.Field)
					}
				}
			}
		}
		encoded, _ := json.Marshal(value)
		if err := ValidatePlainWorkflowInput(name, string(encoded)); err != nil {
			return nil, err
		}
		resolved[name] = value
	}
	return resolved, nil
}

func validateStepInputBindings(bindings map[string]ScriptInputBinding, dependencies []string) error {
	for name, binding := range bindings {
		if !scriptParameterNamePattern.MatchString(name) {
			return fmt.Errorf("input %q needs a name using letters, numbers and underscores", name)
		}
		sources := 0
		if binding.Value != nil {
			sources++
			if !json.Valid(binding.Value) {
				return fmt.Errorf("input %q has invalid JSON", name)
			}
		}
		if binding.Input != "" {
			sources++
		}
		if binding.Output != "" {
			sources++
			if !slices.Contains(dependencies, binding.Output) {
				return fmt.Errorf("input %q must use a declared earlier output", name)
			}
		}
		if sources != 1 || binding.Field != "" && binding.Output == "" {
			return fmt.Errorf("input %q needs exactly one fixed value, workflow input or earlier output", name)
		}
		if err := ValidatePlainWorkflowInput(name, string(binding.Value)); err != nil {
			return err
		}
	}
	return nil
}

// WorkflowStepInputReferences includes nested agent/tool steps as well as the
// main chain, so editing definitions cannot leave a saved mapping dangling.
func WorkflowStepInputReferences(plan *PlanningResponse) map[string][]string {
	result := map[string][]string{}
	if plan == nil {
		return result
	}
	steps := append(append([]PlanStepInterface{}, plan.Steps...), plan.OrphanSteps...)
	for _, info := range collectAllSteps(steps) {
		var bindings map[string]ScriptInputBinding
		switch step := info.Step.(type) {
		case *AgentPlanStep:
			bindings = step.Inputs
		case *ScriptedPlanStep:
			bindings = step.ScriptInputs
		}
		for _, binding := range bindings {
			if binding.Input != "" {
				result[info.Step.GetID()] = append(result[info.Step.GetID()], binding.Input)
			}
		}
	}
	return result
}
