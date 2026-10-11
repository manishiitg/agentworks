package server

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Captured identity is trusted registration context, not a model argument.
// Authorization is recomputed on every call, including retained sessions.
type workflowTeamRegistrar struct {
	definitionRegistrar
	root, user string
}

func (r workflowTeamRegistrar) RegisterCustomTool(n, d string, p map[string]interface{}, e func(context.Context, map[string]interface{}) (string, error), g string) error {
	return r.RegisterCustomToolWithTimeout(n, d, p, e, 0, g)
}
func (r workflowTeamRegistrar) RegisterCustomToolWithTimeout(n, d string, p map[string]interface{}, e func(context.Context, map[string]interface{}) (string, error), t time.Duration, g string) error {
	if teamPlanTool(n) {
		copy := map[string]interface{}{}
		for k, v := range p {
			copy[k] = v
		}
		props := map[string]interface{}{}
		if old, ok := p["properties"].(map[string]interface{}); ok {
			for k, v := range old {
				props[k] = v
			}
		}
		props["expected_plan_revision"] = map[string]interface{}{"type": "string", "description": "For team workflows, use plan_revision from get_workflow_team before proposing a change. Stale edits fail."}
		copy["properties"] = props
		p = copy
	}
	return r.definitionRegistrar.RegisterCustomToolWithTimeout(n, d, p, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = teamUserContext(ctx, r.user)
		m, ok, err := ReadWorkflowManifest(ctx, r.root)
		if err != nil {
			return "", err
		}
		if ok && m.Team != nil {
			access := userAccessForClaims(&UserClaims{UserID: r.user})
			if access.Disabled || workflowAccessForManifest(&UserClaims{UserID: r.user}, m) == WorkflowAccessNone {
				return "", fmt.Errorf("no current workflow team access")
			}
			ctx = withTeamPlanReceipt(ctx)
			member := m.Team.member(r.user)
			if r.user != m.Team.LeaderID {
				if teamRunTool(n) {
					if member == nil || !member.Run {
						return "", fmt.Errorf("route run permission is required")
					}
					if n != "execute_step" && n != "debug_step" {
						return "", fmt.Errorf("Contributors execute assigned steps; whole-workflow runs require the Leader")
					}
					raw, _, err := readFileFromWorkspace(ctx, r.root+"/planning/plan.json")
					if err != nil {
						return "", err
					}
					plan, err := teamJSON(raw)
					if err != nil {
						return "", err
					}
					id := stringToolArg(args, "step_id")
					allowed := false
					for _, a := range m.Team.Routes {
						if a.ContributorID == r.user && teamRouteSteps(plan, teamFindRoute(plan, a))[id] {
							allowed = true
						}
					}
					if !allowed {
						return "", fmt.Errorf("step is not in your assigned route")
					}
				}
				if n == "execute_sql" || n == "configure_goal_metrics" || n == "update_variable" || n == "add_group" || n == "update_group" || n == "delete_group" || n == "update_workflow_config" || n == "set_workflow_contract_version" || n == "set_workflow_llm_config" || strings.HasPrefix(n, "configure_workflow") || n == "set_workflow_identity" {
					return "", fmt.Errorf("shared goal and workflow settings require the Leader")
				}
			}
			if teamPlanTool(n) {
				if !access.CanEdit {
					return "", fmt.Errorf("account does not allow edits")
				}
				ctx = prepareTeamEdit(ctx, r.root, stringToolArg(args, "expected_plan_revision"))
				copy := map[string]interface{}{}
				for k, v := range args {
					if k != "expected_plan_revision" {
						copy[k] = v
					}
				}
				args = copy
			}
		}
		result, err := e(ctx, args)
		if err == nil && ok && m.Team != nil && teamPlanTool(n) {
			err = commitTeamEdit(ctx, result)
		}
		return result, err
	}, t, g)
}
func teamPlanTool(n string) bool {
	return n == "add_step" || n == "update_step" || n == "delete_plan_steps" || n == "manage_step_route" || n == "change_step_type" || n == "update_step_config" || n == "update_step_configs" || n == "maintain_plan" || n == "cleanup_orphan_step_configs" || n == "convert_routing_branch_step_type" || n == "strip_declared_execution_mode" || n == "create_plan" || strings.HasPrefix(n, "migrate_") || n == "restore_step_from_changelog" || n == "update_validation_schema" || strings.HasPrefix(n, "delete_") && (strings.Contains(n, "step") || strings.Contains(n, "route")) || strings.HasPrefix(n, "add_") && strings.Contains(n, "route") || strings.HasPrefix(n, "update_") && strings.Contains(n, "route") || strings.HasPrefix(n, "add_") && strings.Contains(n, "step") || strings.HasPrefix(n, "update_") && strings.Contains(n, "step")
}
func teamRunTool(n string) bool {
	return n == "execute_step" || n == "execute_workflow" || n == "start_workflow_execution" || n == "run_workflow" || n == "run_workflow_groups" || n == "run_full_workflow" || n == "debug_step"
}
