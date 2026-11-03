package server

import (
	"context"
	"encoding/json"
	"fmt"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

const retiredWorkflowGroupsMessage = stepworkflow.RetiredWorkflowGroupsMessage

// All entry points validate the same definition and precedence rule. A preset
// is not a route grant and cannot alter the execution account or file roots.
func resolveWorkflowInvocationInputs(ctx context.Context, root, preset string, overrides map[string]string) (map[string]string, error) {
	raw, found, err := readFileFromWorkspace(ctx, root+"/variables/variables.json")
	if err != nil {
		return nil, err
	}
	var manifest *stepworkflow.VariablesManifest
	if found {
		manifest = &stepworkflow.VariablesManifest{}
		if err := json.Unmarshal([]byte(raw), manifest); err != nil {
			return nil, fmt.Errorf("read workflow inputs: %w", err)
		}
	}
	return stepworkflow.ResolveWorkflowInputs(manifest, preset, overrides)
}

// JSON null explicitly clears a preset; omission keeps the existing value.
// PLAT-746-265: do not conflate an explicit clear with a missing update field.
func (r *UpdateScheduleRequest) UnmarshalJSON(data []byte) error {
	type plain UpdateScheduleRequest
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, present := fields["input_preset"]; present && value.InputPreset == nil {
		empty := ""
		value.InputPreset = &empty
	}
	if _, present := fields["inputs"]; present && value.Inputs == nil {
		value.Inputs = map[string]string{}
	}
	if _, present := fields["group_names"]; present {
		value.GroupNames = []string{}
	}
	*r = UpdateScheduleRequest(value)
	return nil
}

func (r *scheduleExecutionInput) UnmarshalJSON(data []byte) error {
	type plain scheduleExecutionInput
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, present := fields["groups"]; present {
		return fmt.Errorf("groups are retired from execution inputs; save input_preset and inputs instead")
	}
	*r = scheduleExecutionInput(value)
	return nil
}

func (r *CreateScheduleRequest) UnmarshalJSON(data []byte) error {
	type plain CreateScheduleRequest
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, present := fields["group_names"]; present {
		value.GroupNames = []string{}
	}
	*r = CreateScheduleRequest(value)
	return nil
}

func (r *ExecutionOptions) UnmarshalJSON(data []byte) error {
	type plain ExecutionOptions
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, present := fields["enabled_group_names"]; present {
		return fmt.Errorf(retiredWorkflowGroupsMessage)
	}
	*r = ExecutionOptions(value)
	return nil
}

func (r *workflowWebhookRequest) UnmarshalJSON(data []byte) error {
	type plain workflowWebhookRequest
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, present := fields["group_names"]; present {
		return fmt.Errorf(retiredWorkflowGroupsMessage)
	}
	*r = workflowWebhookRequest(value)
	return nil
}
