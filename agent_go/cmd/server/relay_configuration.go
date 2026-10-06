package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// relayConfigurationView adapts legacy group values for the configuration UI.
// Reading never rewrites the draft or an immutable published release.
func relayConfigurationView(manifest *VariablesManifest, workflow *WorkflowManifest) error {
	raw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	var variables stepworkflow.VariablesManifest
	if err := json.Unmarshal(raw, &variables); err != nil {
		return err
	}
	legacy := ""
	for _, trigger := range workflow.Schedules {
		if !trigger.IsFunctionTrigger() || !trigger.Enabled {
			continue
		}
		groups := normalizeScheduleGroupNames(trigger.GroupNames)
		if len(groups) > 1 {
			return fmt.Errorf("legacy Relay trigger %q has multiple configuration groups", trigger.Name)
		}
		if len(groups) == 1 {
			if legacy != "" && legacy != groups[0] {
				return fmt.Errorf("legacy Relay triggers use different configurations; consolidate them before editing configuration")
			}
			legacy = groups[0]
		}
	}
	values, err := stepworkflow.ResolveRelayVariableValues(&variables, legacy)
	if err != nil {
		return err
	}
	declared := make(map[string]bool, len(manifest.Variables))
	for i := range manifest.Variables {
		manifest.Variables[i].Value = values[manifest.Variables[i].Name]
		declared[manifest.Variables[i].Name] = true
	}
	// Older group files could carry values absent from the declaration list.
	// Preserve them during flattening instead of silently dropping config.
	var recovered []string
	for name := range values {
		if !declared[name] {
			recovered = append(recovered, name)
		}
	}
	sort.Strings(recovered)
	for _, name := range recovered {
		manifest.Variables = append(manifest.Variables, Variable{Name: name, Value: values[name]})
	}
	manifest.Groups = nil
	return nil
}

// clearRelayConfigurationBindings retires draft-only legacy metadata after an
// explicit configuration save. Published release manifests stay unchanged.
func clearRelayConfigurationBindings(ctx context.Context, workspace string) error {
	workflowWebhookConfigMu.Lock()
	defer workflowWebhookConfigMu.Unlock()
	manifest, found, err := ReadWorkflowManifest(ctx, workspace)
	if err != nil {
		return fmt.Errorf("Relay manifest unavailable while clearing legacy configuration: %w", err)
	}
	if !found || manifest.Kind != "relay" {
		return fmt.Errorf("Relay manifest unavailable while clearing legacy configuration")
	}
	changed := false
	for i := range manifest.Schedules {
		if len(manifest.Schedules[i].GroupNames) != 0 {
			manifest.Schedules[i].GroupNames = nil
			changed = true
		}
	}
	if changed {
		return WriteWorkflowManifest(ctx, workspace, manifest)
	}
	return nil
}
