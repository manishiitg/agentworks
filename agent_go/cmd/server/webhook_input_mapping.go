package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtrigger"
)

func webhookInputDefinition(definitions *VariablesManifest, name string) (*Variable, error) {
	if definitions != nil {
		for _, field := range definitions.Variables {
			if field.Name == name {
				return &field, nil
			}
		}
	}
	return nil, fmt.Errorf("unknown input %q; choose a declared script input", name)
}

func decodeWebhookInput(raw json.RawMessage) (interface{}, error) {
	var value interface{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

// The existing input allowlist also bounds raw-body mappings. A mapping never
// chooses a preset or bypasses the script's type or fixed-value contract.
func validateWebhookInputMappings(ctx context.Context, root string, cfg *WorkflowWebhookConfig) error {
	if cfg.PayloadMappings == nil || len(cfg.PayloadMappings.Inputs) == 0 {
		return nil
	}
	definitions, err := readWorkflowInputDefinitions(ctx, root)
	if err != nil {
		return err
	}
	if err := validateWebhookInputMappingContract(definitions, cfg, nil); err != nil {
		return err
	}
	return validateWebhookVariableNames(ctx, root, webhookMappedInputNames(cfg.PayloadMappings))
}

// Shared by live configuration and the one-off migration's server validator.
// Neither caller may substitute exposed names for the actual mapping contract.
func validateWebhookInputMappingContract(definitions *VariablesManifest, cfg *WorkflowWebhookConfig, fixed map[string]string) error {
	if cfg.PayloadMappings == nil || len(cfg.PayloadMappings.Inputs) == 0 {
		return nil
	}
	if cfg.InputMode != "raw" && cfg.InputMode != "" {
		return fmt.Errorf("payload input mappings require raw input mode")
	}
	for name, mapping := range cfg.PayloadMappings.Inputs {
		if !slices.Contains(cfg.AllowedVariables, name) {
			return fmt.Errorf("input %q is not allowed by this trigger", name)
		}
		if _, exists := fixed[name]; exists {
			return fmt.Errorf("input %q has a fixed value and cannot be overridden", name)
		}
		field, err := webhookInputDefinition(definitions, name)
		if err != nil {
			return err
		}
		source := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(mapping.Source), "$."), ".")
		if source == "" || len(source) > 256 {
			return fmt.Errorf("input %q needs a payload source path", name)
		}
		for _, part := range strings.Split(source, ".") {
			if strings.TrimSpace(part) == "" {
				return fmt.Errorf("input %q has an invalid payload source path", name)
			}
		}
		choices := []json.RawMessage{}
		for _, raw := range mapping.Values {
			choices = append(choices, raw)
		}
		if len(mapping.Default) > 0 {
			choices = append(choices, mapping.Default)
		}
		for _, raw := range choices {
			value, err := decodeWebhookInput(raw)
			if err != nil {
				return fmt.Errorf("input %q has an invalid mapped JSON value", name)
			}
			encoded, err := workflow.WorkflowInputValue(*field, value)
			if err != nil {
				return err
			}
			if len(encoded) > 16384 {
				return fmt.Errorf("input %q exceeds 16 KiB", name)
			}
		}
	}
	return nil
}

func resolveWebhookMappedInputs(s WorkflowSchedule, delivery *WorkflowWebhookDelivery, definitions ...*VariablesManifest) error {
	if s.Webhook == nil || s.Webhook.PayloadMappings == nil {
		return nil
	}
	var declared *VariablesManifest
	if len(definitions) > 0 {
		declared = definitions[0]
	}
	for name, mapping := range s.Webhook.PayloadMappings.Inputs {
		if !slices.Contains(s.Webhook.AllowedVariables, name) {
			return fmt.Errorf("input %q is not allowed by this trigger", name)
		}
		if _, fixed := s.Inputs[name]; fixed {
			return fmt.Errorf("input %q has a fixed value and cannot be overridden", name)
		}
		field, err := webhookInputDefinition(declared, name)
		if err != nil {
			return err
		}
		value, err := workflowtrigger.Value(delivery.Payload, mapping.Source)
		if len(mapping.Values) > 0 && err == nil {
			key, keyErr := workflowtrigger.Scalar(delivery.Payload, mapping.Source)
			if keyErr != nil {
				return fmt.Errorf("input %q mapping source must be a scalar", name)
			}
			raw, found := mapping.Values[key]
			if !found {
				raw = mapping.Default
			}
			if len(raw) == 0 {
				return fmt.Errorf("input %q has no configured value for this payload", name)
			}
			value, err = decodeWebhookInput(raw)
		} else if err != nil && len(mapping.Default) > 0 {
			value, err = decodeWebhookInput(mapping.Default)
		}
		if err != nil {
			return fmt.Errorf("input %q: %w", name, err)
		}
		encoded, err := workflow.WorkflowInputValue(*field, value)
		if err != nil {
			return err
		}
		if len(encoded) > 16384 {
			return fmt.Errorf("input %q exceeds 16 KiB", name)
		}
		if delivery.Variables == nil {
			delivery.Variables = map[string]string{}
		}
		delivery.Variables[name] = encoded
	}
	return nil
}

func webhookMappedInputNames(mappings *WorkflowWebhookPayloadMappings) []string {
	names := []string{}
	if mappings != nil {
		for name := range mappings.Inputs {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
