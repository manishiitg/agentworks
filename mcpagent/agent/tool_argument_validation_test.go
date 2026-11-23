package mcpagent

import (
	"context"
	"strings"
	"testing"
)

func TestValidateDirectToolArgumentsRejectsMissingAndUnknownFieldsBeforeExecution(t *testing.T) {
	called := false
	exec := validateDirectToolArguments("update_message_sequence_step", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"existing_step_id": map[string]interface{}{"type": "string"},
			"reason":           map[string]interface{}{"type": "string"},
		},
		"required": []interface{}{"existing_step_id", "reason"},
	}, func(context.Context, map[string]interface{}) (string, error) {
		called = true
		return "ran", nil
	})

	_, err := exec(context.Background(), map[string]interface{}{
		"step_id": "step-research-and-draft-outreach",
		"reason":  "repair the contract",
	})
	if err == nil {
		t.Fatal("expected argument validation error")
	}
	for _, want := range []string{
		"missing required field(s): existing_step_id",
		"unknown field(s): step_id",
		`get_api_spec(tool_name="update_message_sequence_step")`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
	if called {
		t.Fatal("malformed arguments reached the executor")
	}
}

func TestValidateDirectToolArgumentsExecutesValidCall(t *testing.T) {
	exec := validateDirectToolArguments("update_message_sequence_step", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"existing_step_id": map[string]interface{}{"type": "string", "minLength": float64(1)},
			"reason":           map[string]interface{}{"type": "string"},
		},
		"required": []string{"existing_step_id", "reason"},
	}, func(_ context.Context, args map[string]interface{}) (string, error) {
		return args["existing_step_id"].(string), nil
	})

	got, err := exec(context.Background(), map[string]interface{}{
		"existing_step_id": "step-research-and-draft-outreach",
		"reason":           "repair the contract",
	})
	if err != nil {
		t.Fatalf("valid call failed: %v", err)
	}
	if got != "step-research-and-draft-outreach" {
		t.Fatalf("result = %q", got)
	}
}

func TestValidateDirectToolArgumentsEnforcesAnyOfRequiredFields(t *testing.T) {
	exec := validateDirectToolArguments("validate_plan_change", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"forbidden_references":          map[string]interface{}{"type": "array"},
			"expected_context_dependencies": map[string]interface{}{"type": "object"},
		},
		"anyOf": []interface{}{
			map[string]interface{}{"required": []interface{}{"forbidden_references"}},
			map[string]interface{}{"required": []interface{}{"expected_context_dependencies"}},
		},
	}, func(context.Context, map[string]interface{}) (string, error) {
		return "ran", nil
	})

	_, err := exec(context.Background(), map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "must provide at least one required field set") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRegisterCustomToolStoresSchemaValidatedExecutor(t *testing.T) {
	called := false
	agent := &Agent{
		toolRegistry: directToolRegistry(),
		toolToServer: make(map[string]string),
	}
	err := agent.registerCustomTool("update_message_sequence_step", "update", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"existing_step_id": map[string]interface{}{"type": "string"},
		},
		"required": []interface{}{"existing_step_id"},
	}, func(context.Context, map[string]interface{}) (string, error) {
		called = true
		return "ran", nil
	}, "workflow")
	if err != nil {
		t.Fatalf("register tool: %v", err)
	}
	registered, ok := agent.lookupDirectTool("update_message_sequence_step")
	if !ok {
		t.Fatal("registered tool missing")
	}
	if _, err := registered.Executor(context.Background(), map[string]interface{}{"step_id": "wrong"}); err == nil {
		t.Fatal("registered executor accepted malformed arguments")
	}
	if called {
		t.Fatal("malformed registered call reached underlying executor")
	}
}
