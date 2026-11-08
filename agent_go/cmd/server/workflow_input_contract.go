package server

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

func readWorkflowInputDefinitions(ctx context.Context, root string) (*VariablesManifest, error) {
	binding, err := findCrewExecutable(root)
	if err != nil {
		return nil, err
	}
	if binding != nil && binding.Kind == "script" {
		definition, err := readCrewScript(ctx, root)
		if err != nil {
			return nil, err
		}
		return definition.InputDefinitions(), nil
	}
	var definitions VariablesManifest
	if err := readProjectEntryFile(ctx, root, "variables/variables.json", &definitions); err != nil {
		return nil, err
	}
	return &definitions, nil
}

// Caller contracts select existing definitions. They cannot change the types,
// override fixed values, choose a preset or expose a credential.
func workflowCallerInputSchema(definitions *VariablesManifest, names []string, preset string, fixed map[string]string) (map[string]interface{}, error) {
	values, err := workflow.ResolveWorkflowInputs(definitions, preset, fixed)
	if err != nil {
		return nil, err
	}
	declared := map[string]Variable{}
	for _, input := range definitions.Variables {
		declared[input.Name] = input
	}
	properties := map[string]interface{}{}
	required := []interface{}{}
	for _, name := range names {
		input, ok := declared[name]
		if !ok {
			return nil, fmt.Errorf("input %q no longer exists; update the selected input names", name)
		}
		if _, fixedValue := fixed[name]; fixedValue {
			continue
		}
		property, err := workflow.WorkflowInputProperty(input)
		if err != nil {
			return nil, err
		}
		if value := values[name]; value != "" {
			var typed interface{} = value
			if workflow.WorkflowInputType(input) != "string" {
				decoder := json.NewDecoder(strings.NewReader(value))
				decoder.UseNumber()
				if err := decoder.Decode(&typed); err != nil {
					return nil, err
				}
			}
			property["default"] = typed
		} else if input.Required {
			required = append(required, name)
		}
		properties[name] = property
	}
	// Required values must be provided by a caller, a preset or a fixed value.
	for _, input := range definitions.Variables {
		_, fixedValue := fixed[input.Name]
		if input.Required && strings.TrimSpace(values[input.Name]) == "" && (fixedValue || !slices.Contains(names, input.Name)) {
			return nil, fmt.Errorf("input %q is needed to start; expose it or set a fixed value or default", input.Name)
		}
	}
	schema := map[string]interface{}{"type": "object", "properties": properties, "additionalProperties": false, "description": "Types and defaults come from workflow Inputs. Fixed values are hidden and cannot be overridden."}
	if len(required) > 0 {
		schema["required"] = required
	}
	if _, err := workflow.CompileInputSchema(schema); err != nil {
		return nil, err
	}
	return schema, nil
}

func init() { workflow.ValidateWorkflowInputReferences = validateWorkflowInputReferences }

// Check saved consumers without reading a projected manifest (which itself
// derives function schemas). Never silently drop references after an edit.
func validateWorkflowInputReferences(ctx context.Context, root string, next *VariablesManifest) error {
	previous, err := readWorkflowInputDefinitions(ctx, root)
	if err != nil {
		return err
	}
	old := map[string]Variable{}
	current := map[string]Variable{}
	for _, input := range previous.Variables {
		old[input.Name] = input
	}
	for _, input := range next.Variables {
		current[input.Name] = input
	}
	used := []string{}
	check := func(label string, names, exposed []string, raw json.RawMessage) {
		var fixed scheduleExecutionInput
		if err := json.Unmarshal(raw, &fixed); err != nil {
			used = append(used, label+": invalid saved inputs")
			return
		}
		for name := range fixed.Inputs {
			names = append(names, name)
		}
		for _, name := range names {
			input, exists := current[name]
			before, wasDefined := old[name]
			if !exists || wasDefined && (workflow.WorkflowInputType(input) != workflow.WorkflowInputType(before) || !reflect.DeepEqual(input.Schema, before.Schema)) {
				used = append(used, label)
				return
			}
		}
		values, err := workflow.ResolveWorkflowInputs(next, fixed.InputPreset, fixed.Inputs)
		if err == nil {
			for _, input := range next.Variables {
				if input.Required && strings.TrimSpace(values[input.Name]) == "" && !slices.Contains(exposed, input.Name) {
					err = fmt.Errorf("input %q is needed to start; set a value or expose it on an envelope", input.Name)
					break
				}
			}
		}
		if err != nil {
			used = append(used, label+": "+err.Error())
		}
	}

	binding, err := findCrewExecutable(root)
	if err != nil {
		return err
	}
	// Converted scripts may keep old plan files as history. Only the active
	// plan engine consumes their references; scripts use the final entries.
	if binding == nil || binding.Kind != "script" {
		var plan workflow.PlanningResponse
		if err := readProjectEntryFile(ctx, root, "planning/plan.json", &plan); err != nil {
			return err
		}
		for step, names := range workflow.WorkflowStepInputReferences(&plan) {
			for _, name := range names {
				input, exists := current[name]
				before, wasDefined := old[name]
				if !exists || wasDefined && (workflow.WorkflowInputType(input) != workflow.WorkflowInputType(before) || !reflect.DeepEqual(input.Schema, before.Schema)) {
					used = append(used, "step "+step)
					break
				}
			}
		}
	}
	functions, err := readProjectFunctionDefinitions(ctx, root)
	if err != nil {
		return err
	}
	for _, fn := range functions {
		names := append([]string(nil), fn.Inputs...)
		if fn.InputMapping != nil {
			names = append(names, fn.InputMapping.Variables...)
		}
		exposed := fn.Inputs
		if fn.InputMapping != nil {
			exposed = nil
			if fn.InputMapping.Mode == "envelope" {
				exposed = fn.InputMapping.Variables
			} else {
				exposed = webhookMappedInputNames(fn.InputMapping.Payload)
			}
		}
		check("function "+fn.Name, names, exposed, fn.Input)
		if err := validateWorkflowFunctionInputContract(next, fn); err != nil {
			used = append(used, err.Error())
		}
	}
	hooks, err := readProjectWebhookDefinitions(ctx, root)
	if err != nil {
		return err
	}
	for _, hook := range hooks {
		var exposed []string
		if hook.InputMapping.Mode == "envelope" {
			exposed = hook.InputMapping.Variables
		} else {
			exposed = webhookMappedInputNames(hook.InputMapping.Payload)
		}
		check("webhook "+hook.Name, append([]string(nil), hook.InputMapping.Variables...), exposed, hook.Input)
		entry := projectFunctionDefinition{Name: hook.Name, Action: hook.Action, Input: hook.Input, InputMapping: &hook.InputMapping}
		if err := validateWorkflowFunctionInputContract(next, entry); err != nil {
			used = append(used, "webhook "+hook.Name+": "+err.Error())
		}
	}
	schedules, err := readProjectSchedules(ctx, root)
	if err != nil {
		return err
	}
	for _, schedule := range schedules {
		check("schedule "+schedule.Name, nil, nil, schedule.Input)
	}
	if len(used) > 0 {
		slices.Sort(used)
		used = slices.Compact(used)
		if binding != nil && binding.Kind == "script" {
			return fmt.Errorf("these input changes affect saved entry points: %s. Save Inputs and their entry_mappings together", strings.Join(used, ", "))
		}
		return fmt.Errorf("these input changes affect saved steps or entry points. Used by: %s. Update them first, then save Inputs", strings.Join(used, ", "))
	}
	return nil
}

func resolveWorkflowWebhookInputs(ctx context.Context, root string, sched WorkflowSchedule, delivery *WorkflowWebhookDelivery) error {
	definitions, err := readWorkflowInputDefinitions(ctx, root)
	if err != nil {
		return err
	}
	for name := range delivery.Variables {
		if _, fixed := sched.Inputs[name]; fixed {
			return fmt.Errorf("input %q has a fixed value and cannot be overridden", name)
		}
	}
	overrides := map[string]string{}
	for name, value := range sched.Inputs {
		overrides[name] = value
	}
	for name, value := range delivery.Variables {
		overrides[name] = value
	}
	values, err := workflow.ResolveWorkflowInputs(definitions, sched.InputPreset, overrides)
	if err != nil {
		return err
	}
	return workflow.ValidateWorkflowLaunchInputs(definitions, values)
}
