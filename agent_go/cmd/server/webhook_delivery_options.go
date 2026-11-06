package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtrigger"
)

func keysWebhookVariables(values map[string]string) []string {
	out := []string{}
	for k := range values {
		out = append(out, k)
	}
	return out
}

func resolveWebhookDeliveryOptions(s WorkflowSchedule, input *WorkflowWebhookDelivery, definitions ...*VariablesManifest) error {
	if s.Webhook != nil && s.Webhook.PayloadMappings != nil {
		if s.Webhook.InputMode == "envelope" {
			return fmt.Errorf("payload mappings require raw input mode")
		}
		if err := resolveWebhookPayloadMappings(s.Webhook.PayloadMappings, input); err != nil {
			return err
		}
	}
	if s.Webhook == nil || s.Webhook.InputMode != "envelope" {
		return nil
	}
	var envelope struct {
		Variables map[string]interface{} `json:"variables"`
		Payload   json.RawMessage        `json:"payload"`
	}
	decoder := json.NewDecoder(bytes.NewReader(input.Payload))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if !strings.HasPrefix(strings.TrimSpace(string(input.Payload)), "{") {
		return fmt.Errorf("webhook envelope must be an object")
	}
	if err := decoder.Decode(&envelope); err != nil {
		return fmt.Errorf("invalid webhook envelope: use declared input values and payload; group selection is retired")
	}

	input.Variables = map[string]string{}
	for k, raw := range envelope.Variables {
		var valueDefinition *Variable
		if len(definitions) > 0 && definitions[0] != nil {
			for _, field := range definitions[0].Variables {
				if field.Name == k {
					copy := field
					valueDefinition = &copy
					break
				}
			}
			if valueDefinition == nil {
				return fmt.Errorf("unknown input %q", k)
			}
		}
		v, ok := raw.(string)
		if !ok {
			encoded, err := json.Marshal(raw)
			if err != nil {
				return err
			}
			v = string(encoded)
		}
		if !slices.Contains(s.Webhook.AllowedVariables, k) {
			return fmt.Errorf("variable %q is not allowed by this trigger", k)
		}
		if len(v) > 16384 {
			return fmt.Errorf("variable %q exceeds 16 KiB", k)
		}
		if valueDefinition != nil {
			// Text fields must receive text, rather than stringify arbitrary JSON.
			checked, err := workflow.WorkflowInputValue(*valueDefinition, raw)
			if err != nil {
				return err
			}
			v = checked
		}
		input.Variables[k] = v
	}
	if len(envelope.Payload) == 0 {
		input.Payload = json.RawMessage(`{}`)
	} else {
		input.Payload = envelope.Payload
	}
	return nil
}

func webhookMappingValue(payload json.RawMessage, mapping WorkflowWebhookValueMapping) (string, error) {
	sourceValue, err := workflowtrigger.Scalar(payload, mapping.Source)
	if err != nil {
		return "", err
	}
	if selected, ok := mapping.Values[sourceValue]; ok && strings.TrimSpace(selected) != "" {
		return strings.TrimSpace(selected), nil
	}
	if fallback := strings.TrimSpace(mapping.Default); fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("payload mapping source %q has no configured value for %q", mapping.Source, sourceValue)
}

func resolveWebhookPayloadMappings(mappings *WorkflowWebhookPayloadMappings, input *WorkflowWebhookDelivery) error {
	if mappings == nil {
		return nil
	}
	if mappings.Group != nil {
		return fmt.Errorf(retiredWorkflowGroupsMessage)
	}
	if len(mappings.Routes) > 0 {
		input.RouteSelections = make(map[string]string, len(mappings.Routes))
		for stepID, mapping := range mappings.Routes {
			routeID, err := webhookMappingValue(input.Payload, mapping)
			if err != nil {
				return fmt.Errorf("route mapping for step %q: %w", stepID, err)
			}
			input.RouteSelections[stepID] = routeID
		}
	}
	if mappings.Step != nil {
		stepID, err := webhookMappingValue(input.Payload, *mappings.Step)
		if err != nil {
			return fmt.Errorf("step mapping: %w", err)
		}
		input.StepID = stepID
	}
	return nil
}

func resolvedWebhookExecutionTarget(s WorkflowSchedule, input *WorkflowWebhookDelivery) (string, map[string]string) {
	routes := make(map[string]string, len(s.RouteSelections)+len(input.RouteSelections))
	for stepID, routeID := range s.RouteSelections {
		routes[stepID] = routeID
	}
	for stepID, routeID := range input.RouteSelections {
		routes[stepID] = routeID
	}
	stepID := ""
	if s.Webhook != nil {
		stepID = s.Webhook.StepID
	}
	if input.StepID != "" {
		stepID = input.StepID
	}
	return stepID, routes
}

func validateWebhookVariableNames(ctx context.Context, workspace string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	definitions, err := readWorkflowInputDefinitions(ctx, workspace)
	if err != nil {
		return err
	}
	vars := *definitions
	declared := map[string]bool{}
	for _, v := range vars.Variables {
		declared[v.Name] = true
	}
	manifest, found, err := ReadWorkflowManifest(ctx, workspace)
	if err != nil || !found {
		return fmt.Errorf("cannot validate secret boundaries")
	}
	blocked := map[string]bool{}
	if selected := manifest.Capabilities.SelectedGlobalSecretNames; selected != nil {
		for _, v := range *selected {
			blocked[vaultSecretShortName(v)] = true
		}
	}
	for _, v := range getGlobalSecrets() {
		blocked[v.Name] = true
	}
	for _, name := range names {
		if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`).MatchString(name) || !declared[name] || blocked[name] {
			return fmt.Errorf("input %q is undeclared or protected", name)
		}
		if err := validateWorkflowExposedInput(name); err != nil {
			return err
		}
	}

	return nil
}

// Concurrent invocations must never attribute another lane's failure to this run.
func webhookInvocationFolders(folders []RunFolderInfo, folder string, hook bool) []RunFolderInfo {
	result := []RunFolderInfo{}
	for _, f := range folders {
		base := strings.Split(f.Name, "/")[0]
		if hook {
			if base == folder {
				result = append(result, f)
			}
		} else if folder == "" || base == folder {
			result = append(result, f)
		}
	}
	return result
}

// scheduleInvocationFolders isolates reconciliation to the immutable folder
// bound to this occurrence so a concurrent schedule cannot donate its result.
func scheduleInvocationFolders(folders []RunFolderInfo, folder string, scheduled bool) []RunFolderInfo {
	if !scheduled {
		return folders
	}
	result := []RunFolderInfo{}
	for _, candidate := range folders {
		if strings.Split(candidate.Name, "/")[0] == folder {
			result = append(result, candidate)
		}
	}
	return result
}
