package step_based_workflow

import (
	"context"
	"time"
)

// ScheduleCollisionCheck returns a structured warning before any side effect.
// The server supplies the live scheduler lookup; children inherit the callback.
type ScheduleCollisionCheck func(context.Context, string, map[string]interface{}) error

type collisionToolRegistrar struct {
	DefinitionToolRegistrar
	check ScheduleCollisionCheck
}

func ScheduleGuardedTool(name string) bool {
	switch name {
	case "add_step", "update_step", "manage_step_route", "manage_group", "maintain_plan", "execute_step", "run_full_workflow", "debug_step", "convert_routing_branch_step_type",
		"create_plan", "change_step_type", "delete_plan_steps", "cleanup_orphan_step_configs",
		"add_scripted_step", "add_agent_step", "add_routing_step", "add_branch_step",
		"add_human_input_step", "add_agent_route",
		"add_crew_step",
		"update_scripted_step", "update_agent_step", "update_routing_step", "update_branch_step",
		"update_human_input_step", "update_agent_route",
		"update_crew_step",
		"delete_agent_route", "update_step_config", "update_validation_schema",
		"migrate_agent_code_items",
		"migrate_orchestrator_step_type", "migrate_declared_execution_mode", "strip_declared_execution_mode",
		"update_workflow_config", "set_workflow_llm_config", "update_variable", "add_group", "update_group", "delete_group":
		return true
	}
	return false
}

func GuardScheduleTools(r DefinitionToolRegistrar, check ScheduleCollisionCheck) DefinitionToolRegistrar {
	if check == nil {
		return r
	}
	return collisionToolRegistrar{r, check}
}

func (r collisionToolRegistrar) RegisterCustomTool(name, description string, schema map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), group string) error {
	return r.RegisterCustomToolWithTimeout(name, description, schema, execute, 0, group)
}

func (r collisionToolRegistrar) RegisterCustomToolWithTimeout(name, description string, schema map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, group string) error {
	if ScheduleGuardedTool(name) {
		copySchema := make(map[string]interface{}, len(schema))
		for k, v := range schema {
			copySchema[k] = v
		}
		props := map[string]interface{}{}
		declaresForce := false
		if original, ok := schema["properties"].(map[string]interface{}); ok {
			for k, v := range original {
				props[k] = v
			}
			_, declaresForce = original["force"]
		}
		props["force"] = map[string]interface{}{"type": "boolean", "description": "Default false. Only set true after showing a schedule_running warning and receiving explicit user approval to proceed concurrently. Does not stop the schedule or bypass permissions."}
		copySchema["properties"] = props
		schema = copySchema
		original := execute
		execute = func(ctx context.Context, args map[string]interface{}) (string, error) {
			if err := r.check(ctx, name, args); err != nil {
				return "", err
			}
			// `force` belongs to the guard. A tool with a strict argument schema (update_step) rejects it as
			// an unknown property, so an approved override could never run. A tool that declares its own
			// `force` keeps it.
			if _, hasForce := args["force"]; hasForce && !declaresForce {
				trimmed := make(map[string]interface{}, len(args))
				for k, v := range args {
					if k != "force" {
						trimmed[k] = v
					}
				}
				args = trimmed
			}
			return original(ctx, args)
		}
	}
	return r.DefinitionToolRegistrar.RegisterCustomToolWithTimeout(name, description, schema, execute, timeout, group)
}

type collisionRegistrar struct {
	DefinitionRegistrar
	DefinitionToolRegistrar
}

// Explicit forwarding avoids ambiguity between embedded registration methods.
func (r collisionRegistrar) RegisterCustomTool(n, d string, s map[string]interface{}, f func(context.Context, map[string]interface{}) (string, error), g string) error {
	return r.DefinitionToolRegistrar.RegisterCustomTool(n, d, s, f, g)
}
func (r collisionRegistrar) RegisterCustomToolWithTimeout(n, d string, s map[string]interface{}, f func(context.Context, map[string]interface{}) (string, error), t time.Duration, g string) error {
	return r.DefinitionToolRegistrar.RegisterCustomToolWithTimeout(n, d, s, f, t, g)
}

func guardScheduleRegistrar(r DefinitionRegistrar, check ScheduleCollisionCheck) DefinitionRegistrar {
	if check == nil {
		return r
	}
	return collisionRegistrar{r, GuardScheduleTools(r, check)}
}
