package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The server installs reference validation once. Standalone executors do not
// own the function/schedule stores; their launch validation still uses this contract.
var ValidateWorkflowInputReferences = func(context.Context, string, *VariablesManifest) error { return nil }

func WorkflowInputType(input Variable) string {
	if input.Type != "" {
		return input.Type
	}
	if kind, _ := input.Schema["type"].(string); kind != "" {
		return kind
	}
	return "string"
}

// CompileInputSchema preserves the existing local-reference boundary: a schema
// cannot fetch URLs or read host files. Each compiler owns its resources.
func CompileInputSchema(value map[string]interface{}) (*jsonschema.Schema, error) {
	if externalInputRef(value) {
		return nil, fmt.Errorf("only local schema references are supported")
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(jsonschema.SchemeURLLoader{})
	const location = "https://workflow-input.invalid/contract"
	if err := compiler.AddResource(location, value); err != nil {
		return nil, err
	}
	return compiler.Compile(location)
}

func externalInputRef(value interface{}) bool {
	switch v := value.(type) {
	case map[string]interface{}:
		for key, item := range v {
			if key == "$ref" || key == "$dynamicRef" || key == "$recursiveRef" {
				ref, ok := item.(string)
				if !ok || !strings.HasPrefix(ref, "#") {
					return true
				}
			}
			if externalInputRef(item) {
				return true
			}
		}
	case []interface{}:
		for _, item := range v {
			if externalInputRef(item) {
				return true
			}
		}
	}
	return false
}

func WorkflowInputProperty(input Variable) (map[string]interface{}, error) {
	kind := WorkflowInputType(input)
	switch kind {
	case "string", "integer", "number", "boolean", "object", "array":
	default:
		return nil, fmt.Errorf("input %q has an unsupported type", input.Name)
	}
	property := map[string]interface{}{}
	if input.Schema != nil {
		raw, err := json.Marshal(input.Schema)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &property); err != nil {
			return nil, err
		}
		if declared, ok := property["type"]; ok && declared != kind {
			return nil, fmt.Errorf("input %q has conflicting types", input.Name)
		}
	}
	property["type"] = kind
	if input.Required && kind == "string" {
		constraints, _ := property["allOf"].([]interface{})
		property["allOf"] = append(constraints, map[string]interface{}{"type": "string", "pattern": `\S`})
	}
	if input.Description != "" {
		property["description"] = input.Description
	}
	// Give each property its own resource scope when composing a function's
	// schema, so #/$defs references keep the same meaning as standalone validation.
	property["$id"] = "https://workflow-input.invalid/fields/" + url.PathEscape(input.Name)
	if _, err := CompileInputSchema(property); err != nil {
		return nil, fmt.Errorf("input %q schema: %w", input.Name, err)
	}
	return property, nil
}

// WorkflowInputValue is the shared conversion and validation rule. Runtime
// environments retain their existing string encoding; callers use typed JSON.
func WorkflowInputValue(input Variable, raw interface{}) (string, error) {
	var text string
	if value, ok := raw.(string); ok {
		text = value
	} else {
		if WorkflowInputType(input) == "string" {
			return "", fmt.Errorf("input %q must be text", input.Name)
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			return "", fmt.Errorf("input %q is invalid", input.Name)
		}
		text = string(encoded)
	}
	if len(text) > 16384 {
		return "", fmt.Errorf("input %q exceeds 16 KiB", input.Name)
	}
	if err := ValidatePlainWorkflowInput(input.Name, text); err != nil {
		return "", err
	}
	value := interface{}(text)
	if WorkflowInputType(input) != "string" {
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return "", fmt.Errorf("input %q must be %s", input.Name, WorkflowInputType(input))
		}
		var extra interface{}
		if err := decoder.Decode(&extra); err != io.EOF {
			return "", fmt.Errorf("input %q must contain one JSON value", input.Name)
		}
	}
	property, err := WorkflowInputProperty(input)
	if err != nil {
		return "", err
	}
	schema, err := CompileInputSchema(property)
	if err != nil {
		return "", err
	}
	if err := schema.Validate(value); err != nil {
		return "", fmt.Errorf("input %q: %w", input.Name, err)
	}
	return text, nil
}

func ValidateWorkflowLaunchInputs(manifest *VariablesManifest, values map[string]string) error {
	if manifest == nil {
		return nil
	}
	for _, input := range manifest.Variables {
		if input.Required && strings.TrimSpace(values[input.Name]) == "" {
			return fmt.Errorf("missing required input %q; supply it or set a workflow default", input.Name)
		}
	}
	return nil
}
