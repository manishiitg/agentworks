package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// Preserve the stable detached API aliases while adding the current website
// operations. Only explicitly named plan mutations cross this transport;
// account, execution and arbitrary workspace tools are never captured.
var externalBuilderPlanNames = []string{"create_plan", "add_step", "update_step", "manage_step_route", "convert_routing_branch_step_type", "maintain_plan", "restore_step_from_changelog", "migrate_workflow_skills"}

type externalBuilderPlanCollector struct{ tools map[string]capturedPlanTool }

func (r *externalBuilderPlanCollector) RegisterCustomTool(n, d string, s map[string]interface{}, e func(context.Context, map[string]interface{}) (string, error), g string) error {
	return r.RegisterCustomToolWithTimeout(n, d, s, e, 0, g)
}
func (r *externalBuilderPlanCollector) RegisterCustomToolWithTimeout(n, d string, s map[string]interface{}, e func(context.Context, map[string]interface{}) (string, error), _ time.Duration, _ string) error {
	allowed := false
	for _, name := range externalBuilderPlanNames {
		allowed = allowed || n == name
	}
	if !allowed {
		return nil
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	var schema map[string]interface{}
	if err = json.Unmarshal(raw, &schema); err != nil {
		return err
	}
	// A tool without parameters serializes required as null, which is not a
	// valid JSON Schema keyword value.
	if required, present := schema["required"]; present && required == nil {
		delete(schema, "required")
	}
	closeExternalPlanSchema(schema)
	schema["additionalProperties"] = false
	validator, err := compilePlanToolSchema(schema)
	if err != nil {
		return err
	}
	r.tools[n] = capturedPlanTool{description: d, schema: schema, validator: validator, execute: e}
	return nil
}

func externalBuilderPlanRegistry(workspace string, logger loggerv2.Logger, read func(context.Context, string) (string, error), write func(context.Context, string, string) error, move func(context.Context, string, string) error) (*externalBuilderPlanCollector, error) {
	r := &externalBuilderPlanCollector{tools: map[string]capturedPlanTool{}}
	err := RegisterPlanModificationTools(r, workspace, logger, nativePlanReadFile(read), withPlanMutationWriteAccess(workspace, write), move, "agentworks-api")
	return r, err
}

func ExternalBuilderPlanToolDefinitions() []ExternalPlanToolDefinition {
	definitions := ExternalPlanToolDefinitions()
	r, err := externalBuilderPlanRegistry("Workflow/schema", loggerv2.NewNoop(), func(context.Context, string) (string, error) { return "", fmt.Errorf("schema only") }, func(context.Context, string, string) error { return fmt.Errorf("schema only") }, nil)
	if err != nil {
		panic(err)
	}
	for _, name := range externalBuilderPlanNames {
		tool, ok := r.tools[name]
		if !ok {
			panic("missing native plan operation: " + name)
		}
		raw, err := json.Marshal(tool.schema)
		if err != nil {
			panic(err)
		}
		definitions = append(definitions, ExternalPlanToolDefinition{name, tool.description, raw})
	}
	return definitions
}

func ExecuteExternalBuilderPlanTool(ctx context.Context, name string, args map[string]interface{}, workspace string, logger loggerv2.Logger, read func(context.Context, string) (string, error), write func(context.Context, string, string) error, move func(context.Context, string, string) error) (string, error) {
	modern := false
	for _, n := range externalBuilderPlanNames {
		modern = modern || name == n
	}
	if !modern {
		return ExecuteExternalPlanTool(ctx, name, args, workspace, logger, read, write, move)
	}
	if read == nil || write == nil {
		return "", fmt.Errorf("plan callbacks are required")
	}
	if logger == nil {
		logger = loggerv2.NewNoop()
	}
	if move == nil {
		move = func(context.Context, string, string) error { return fmt.Errorf("workspace move is unavailable") }
	}
	r, err := externalBuilderPlanRegistry(workspace, logger, read, write, move)
	if err != nil {
		return "", err
	}
	tool := r.tools[name]
	raw, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	var normalized map[string]interface{}
	if err = json.Unmarshal(raw, &normalized); err != nil {
		return "", err
	}
	if err = tool.validator.Validate(normalized); err != nil {
		return "", fmt.Errorf("invalid %s arguments: %w", name, err)
	}
	return tool.execute(withPlanChangeOrigin(ctx, "agentworks-api"), normalized)
}
