package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Resolve declared inputs before entering the existing direct-run lifecycle.
func directScriptRunOptions(ctx context.Context, args map[string]interface{}, target DiscoveredWorkflow) (*workflow.ExecutionOptions, error) {
	for _, retired := range []string{"group_name", "enabled_group_names", "variables", "run_folder", "selected_run_folder", "resume_from_step", "human_inputs", "route_selections", "mock_agents"} {
		if _, ok := args[retired]; ok {
			return nil, fmt.Errorf("Start a fresh script run with inputs and input_preset; %s is not a start argument.", retired)
		}
	}
	preset := ""
	if raw, ok := args["input_preset"]; ok {
		var valid bool
		preset, valid = raw.(string)
		if !valid {
			return nil, fmt.Errorf("input_preset must be a string")
		}
	}
	definitions, err := readWorkflowInputDefinitions(ctx, target.WorkspacePath)
	if err != nil {
		return nil, err
	}
	supplied := map[string]interface{}{}
	if raw, ok := args["inputs"]; ok {
		var valid bool
		supplied, valid = raw.(map[string]interface{})
		if !valid {
			return nil, fmt.Errorf("inputs must be an object")
		}
	}
	inputs := map[string]string{}
	for name, value := range supplied {
		found := false
		for _, definition := range definitions.Variables {
			if definition.Name == name {
				found = true
				inputs[name], err = workflow.WorkflowInputValue(definition, value)
				break
			}
		}
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("Unknown input %q; use this script's declared inputs.", name)
		}
	}
	resolved, err := workflow.ResolveWorkflowInputs(definitions, preset, inputs)
	if err == nil {
		err = workflow.ValidateWorkflowLaunchInputs(definitions, resolved)
	}
	if err != nil {
		return nil, err
	}
	return &workflow.ExecutionOptions{InputPreset: preset, Inputs: inputs, ResolvedInputs: resolved, SelectedRunFolder: "run-" + strings.ReplaceAll(uuid.NewString(), "-", ""), RunFolderAllocated: true, ExecutionStrategy: "start_from_beginning_no_human"}, nil
}
