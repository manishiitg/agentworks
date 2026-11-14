package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Input edits select existing entries, never their identity, caller, auth or clock.
type scriptEntryInputPatch struct {
	Inputs       *[]string                   `json:"inputs,omitempty"`
	Input        *scriptEntryFixedInputs     `json:"input,omitempty"`
	InputMapping *projectWebhookInputMapping `json:"input_mapping,omitempty"`
}

type scriptEntryFixedInputs struct {
	InputPreset *string            `json:"input_preset,omitempty"`
	Inputs      *map[string]string `json:"inputs,omitempty"`
}

type scriptEntryInputEdits struct {
	Functions map[string]scriptEntryInputPatch `json:"functions,omitempty"`
	Webhooks  map[string]scriptEntryInputPatch `json:"webhooks,omitempty"`
}

func scriptEntryInputEditsSchema() map[string]interface{} {
	patch := map[string]interface{}{"type": "object", "additionalProperties": false, "properties": map[string]interface{}{
		"inputs": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Function caller input names; use input_mapping.variables for a webhook or bound function."},
		"input": map[string]interface{}{"type": "object", "additionalProperties": false, "properties": map[string]interface{}{
			"input_preset": map[string]interface{}{"type": "string"},
			"inputs":       map[string]interface{}{"type": "object", "additionalProperties": map[string]interface{}{"type": "string"}},
		}},
		"input_mapping": scriptEntryInputMappingSchema(),
	}}
	return map[string]interface{}{"type": "object", "additionalProperties": false, "description": "Optional input-only edits keyed by existing entry IDs. Saved with the script inputs after validating the complete result. Omitted entries and fields stay unchanged.", "properties": map[string]interface{}{
		"functions": map[string]interface{}{"type": "object", "additionalProperties": patch},
		"webhooks":  map[string]interface{}{"type": "object", "additionalProperties": patch},
	}}
}

func scriptEntryInputMappingSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "additionalProperties": false, "properties": map[string]interface{}{
		"mode":      map[string]interface{}{"type": "string", "enum": []interface{}{"raw", "envelope"}},
		"variables": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
		"payload":   map[string]interface{}{"type": "object", "description": "Complete saved payload mapping, including inputs with source and optional values/default."},
	}}
}

func decodeScriptEntryInputEdits(value interface{}) (*scriptEntryInputEdits, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var edits scriptEntryInputEdits
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&edits); err != nil {
		return nil, fmt.Errorf("entry_mappings accepts only saved input fields: %w", err)
	}
	return &edits, nil
}

func patchScriptEntryInput(raw json.RawMessage, patch *scriptEntryFixedInputs) (json.RawMessage, error) {
	if patch == nil {
		return raw, nil
	}
	var input scheduleExecutionInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("invalid saved entry inputs: %w", err)
	}
	if patch.InputPreset != nil {
		input.InputPreset = *patch.InputPreset
	}
	if patch.Inputs != nil {
		input.Inputs = *patch.Inputs
	}
	return json.Marshal(input)
}

func validateScriptEntryInputPatch(patch scriptEntryInputPatch) error {
	if patch.InputMapping != nil && patch.InputMapping.Payload != nil {
		payload := patch.InputMapping.Payload
		if payload.Step != nil || payload.Group != nil || len(payload.Routes) > 0 {
			return fmt.Errorf("map declared script inputs; script input edits do not select plan steps or routes")
		}
	}
	return nil
}

func stageScriptEntryInputEdits(ctx context.Context, root string, edits *scriptEntryInputEdits) error {
	if edits == nil {
		return nil
	}
	functions, err := readProjectFunctionDefinitions(ctx, root)
	if err != nil {
		return err
	}
	for id, patch := range edits.Functions {
		if err := validateScriptEntryInputPatch(patch); err != nil {
			return err
		}
		found := false
		for i := range functions {
			fn := &functions[i]
			if fn.ID != id {
				continue
			}
			found = true
			if patch.InputMapping != nil && fn.InputMapping == nil {
				return fmt.Errorf("function %q has no bound mapping; keep its caller input contract", fn.Name)
			}
			if patch.Inputs != nil {
				fn.Inputs = *patch.Inputs
			}
			if patch.InputMapping != nil {
				fn.InputMapping = patch.InputMapping
			}
			fn.Input, err = patchScriptEntryInput(fn.Input, patch.Input)
			if err != nil {
				return fmt.Errorf("function %q: %w", fn.Name, err)
			}
		}
		if !found {
			return fmt.Errorf("function %q is not saved in this workflow; choose its existing entry ID", id)
		}
	}
	if len(edits.Functions) > 0 {
		if err := writeProjectFunctionDefinitions(ctx, root, functions); err != nil {
			return err
		}
	}
	hooks, err := readProjectWebhookDefinitions(ctx, root)
	if err != nil {
		return err
	}
	for id, patch := range edits.Webhooks {
		if err := validateScriptEntryInputPatch(patch); err != nil {
			return err
		}
		if patch.Inputs != nil {
			return fmt.Errorf("webhook %q selects input names in input_mapping.variables", id)
		}
		found := false
		for i := range hooks {
			hook := &hooks[i]
			if hook.ID != id {
				continue
			}
			found = true
			if patch.InputMapping != nil {
				hook.InputMapping = *patch.InputMapping
			}
			hook.Input, err = patchScriptEntryInput(hook.Input, patch.Input)
			if err != nil {
				return fmt.Errorf("webhook %q: %w", hook.Name, err)
			}
		}
		if !found {
			return fmt.Errorf("webhook %q is not saved in this workflow; choose its existing entry ID", id)
		}
	}
	if len(edits.Webhooks) > 0 {
		return writeProjectWebhookDefinitions(ctx, root, hooks)
	}
	return nil
}

// PLAT-746-450: validate the complete candidate rather than requiring either
// half of a schema/mapping change to be valid against the old other half.
func saveCrewScriptDefinition(ctx context.Context, binding *crewExecutableBinding, definition *workflow.CrewScriptDefinition, edits *scriptEntryInputEdits) error {
	if definition.ID != binding.ID {
		return fmt.Errorf("keep the saved script identity")
	}
	if err := workflow.ValidateWorkflowInputDefinitions(definition.InputDefinitions()); err != nil {
		return err
	}
	if _, err := crewScriptCapabilities(ctx, binding.CrewRoot, definition); err != nil {
		return err
	}
	ctx = prepareTeamEdit(ctx, binding.Root, "")
	raw, err := json.MarshalIndent(definition, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileToWorkspace(ctx, path.Join(binding.Root, "code", "script.json"), string(raw)); err != nil {
		return err
	}
	if err := stageScriptEntryInputEdits(ctx, binding.Root, edits); err != nil {
		return err
	}
	// Staged reads expose the final script and entry definitions to the same
	// runtime validators. No source or entry file has been changed yet.
	if err := validateWorkflowInputReferences(ctx, binding.Root, definition.InputDefinitions()); err != nil {
		return err
	}
	if err := commitTeamEdit(ctx, `{"success":true}`); err != nil {
		return err
	}
	invalidateWorkflowManifestIndex()
	return nil
}
