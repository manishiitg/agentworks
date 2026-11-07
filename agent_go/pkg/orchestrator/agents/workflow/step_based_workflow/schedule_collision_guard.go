package step_based_workflow

import (
	"context"
	"time"
)

// ScheduleCollisionCheck runs before a workflow-changing tool's side effect (today: a bot turn's grant recheck).
// There is no schedule-overlap guard any more (owner, 5 Nov); children inherit the callback.
type ScheduleCollisionCheck func(context.Context, string, map[string]interface{}) error

type collisionToolRegistrar struct {
	DefinitionToolRegistrar
	check ScheduleCollisionCheck
}

func ScheduleGuardedTool(name string) bool {
	switch name {
	case "add_step", "update_step", "manage_step_route", "maintain_plan", "execute_step", "run_full_workflow", "debug_step", "convert_routing_branch_step_type",
		"create_plan", "delete_plan_steps", "cleanup_orphan_step_configs",
		"add_scripted_step", "add_agent_step", "add_routing_step", "add_branch_step",
		"add_agent_route",
		"update_scripted_step", "update_agent_step", "update_routing_step", "update_branch_step",
		"update_agent_route",
		"delete_agent_route", "update_step_config", "update_validation_schema",
		"migrate_workflow_skills",
		"update_workflow_config", "set_workflow_llm_config", "update_variable":
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
		original := execute
		execute = func(ctx context.Context, args map[string]interface{}) (string, error) {
			if err := r.check(ctx, name, args); err != nil {
				return "", err
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
