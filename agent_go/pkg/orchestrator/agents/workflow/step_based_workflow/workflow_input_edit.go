package step_based_workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// WorkflowInputEditDefinition exposes the Builder's existing single-input edit
// contract to Crew chat, without another implementation or whole-file writes.
func WorkflowInputEditDefinition() WorkflowToolDefinition {
	return WorkflowToolDefinition{Name: "update_variable", Description: "Add, update or explicitly delete one workflow input. Other inputs and saved values stay unchanged. Types, defaults and saved callers are validated.", InputSchema: json.RawMessage(getUpdateVariableSchema())}
}

func ExecuteWorkflowInputEdit(ctx context.Context, args map[string]interface{}, workspace string, logger loggerv2.Logger, read func(context.Context, string) (string, error), write func(context.Context, string, string) error) (string, error) {
	var schema map[string]interface{}
	if err := json.Unmarshal(WorkflowInputEditDefinition().InputSchema, &schema); err != nil {
		return "", err
	}
	closeWorkflowInputSchema(schema)
	validator, err := CompileInputSchema(schema)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	var normalized map[string]interface{}
	if err = json.Unmarshal(raw, &normalized); err != nil {
		return "", err
	}
	if err = validator.Validate(normalized); err != nil {
		return "", err
	}
	if logger == nil {
		logger = loggerv2.NewNoop()
	}
	readInputs := func(ctx context.Context, file string) (string, error) {
		raw, err := read(ctx, path.Join(workspace, file))
		if file == "variables/variables.json" && errors.Is(err, os.ErrNotExist) {
			return `{"variables":[]}`, nil
		}
		return raw, err
	}
	writeInputs := func(ctx context.Context, file, content string) error {
		return write(ctx, path.Join(workspace, file), content)
	}
	return createUpdateVariableExecutor(workspace, logger, readInputs, writeInputs)(ctx, normalized)
}

// WorkflowToolDefinition describes a saved input or entry-point operation.
type WorkflowToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

func closeWorkflowInputSchema(schema map[string]interface{}) {
	if props, ok := schema["properties"].(map[string]interface{}); ok {
		if _, explicit := schema["additionalProperties"]; !explicit {
			schema["additionalProperties"] = false
		}
		for _, raw := range props {
			if child, ok := raw.(map[string]interface{}); ok {
				closeWorkflowInputSchema(child)
			}
		}
	}
	for _, key := range []string{"items", "additionalProperties"} {
		if child, ok := schema[key].(map[string]interface{}); ok {
			closeWorkflowInputSchema(child)
		}
	}
	for _, key := range []string{"anyOf", "allOf", "oneOf"} {
		if children, ok := schema[key].([]interface{}); ok {
			for _, raw := range children {
				if child, ok := raw.(map[string]interface{}); ok {
					closeWorkflowInputSchema(child)
				}
			}
		}
	}
}
