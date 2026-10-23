package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	workspacepkg "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

const GoalAgentContractVersion = "1.0.50"

// Script helpers share the existing typed call/child lifecycle; LLM children no
// longer belong to a Goal step. This applies to legacy saved plans too.
func goalScriptHelperTool(name string) bool {
	switch name {
	case "call_scripted_sub_agent", "get_route_description", "query_sub_agent", "stop_sub_agent", "get_sub_agent_conversation":
		return true
	}
	return false
}

func goalScriptedRoutes(routes []PlanOrchestrationRoute) []PlanOrchestrationRoute {
	out := make([]PlanOrchestrationRoute, 0, len(routes))
	for _, route := range routes {
		if _, ok := route.SubAgentStep.(*ScriptedPlanStep); ok {
			out = append(out, route)
		}
	}
	return out
}

// Migration uses raw JSON to retain unknown fields and the exact old authored
// strings. Notes are audit data, never re-injected as executable instructions.
// There is deliberately no automatic Crew creation or message-phase collapse.
func migrateGoalAgentContent(planContent, configContent string) (string, string, error) {
	var plan, configs map[string]json.RawMessage
	if err := json.Unmarshal([]byte(planContent), &plan); err != nil {
		return "", "", err
	}
	if err := json.Unmarshal([]byte(configContent), &configs); err != nil {
		return "", "", err
	}
	var configSteps []map[string]json.RawMessage
	if err := json.Unmarshal(configs["steps"], &configSteps); err != nil {
		return "", "", err
	}
	byID := map[string]map[string]json.RawMessage{}
	for _, sc := range configSteps {
		var id string
		_ = json.Unmarshal(sc["id"], &id)
		byID[id] = sc
	}
	var orphans []map[string]json.RawMessage
	_ = json.Unmarshal(plan["orphan_steps"], &orphans)
	orphanTypes := map[string]string{}
	for _, orphan := range orphans {
		var id, typ string
		_ = json.Unmarshal(orphan["id"], &id)
		_ = json.Unmarshal(orphan["type"], &typ)
		orphanTypes[id] = typ
	}
	var walk func(json.RawMessage) (json.RawMessage, error)
	walk = func(raw json.RawMessage) (json.RawMessage, error) {
		var step map[string]json.RawMessage
		if err := json.Unmarshal(raw, &step); err != nil {
			return nil, err
		}
		var id, typ string
		_ = json.Unmarshal(step["id"], &id)
		_ = json.Unmarshal(step["type"], &typ)
		note := map[string]json.RawMessage{}
		if len(step["goal_agent_migration"]) > 0 {
			if err := json.Unmarshal(step["goal_agent_migration"], &note); err != nil {
				return nil, err
			}
		}
		if typ == "agent" {
			var routes []map[string]json.RawMessage
			if len(step["predefined_routes"]) > 0 {
				if err := json.Unmarshal(step["predefined_routes"], &routes); err != nil {
					return nil, err
				}
			}
			kept, retired := []map[string]json.RawMessage{}, []map[string]json.RawMessage{}
			for _, route := range routes {
				if raw := route["sub_agent_step"]; len(raw) > 0 && string(raw) != "null" {
					migrated, err := walk(raw)
					if err != nil {
						return nil, err
					}
					route["sub_agent_step"] = migrated
				}
				var child map[string]json.RawMessage
				_ = json.Unmarshal(route["sub_agent_step"], &child)
				var childType, ref string
				_ = json.Unmarshal(child["type"], &childType)
				_ = json.Unmarshal(route["orphan_step_ref"], &ref)
				if ref != "" {
					childType = orphanTypes[ref]
				}
				if childType == "scripted" {
					kept = append(kept, route)
				} else {
					retired = append(retired, route)
				}
			}
			if len(retired) > 0 {
				note["retired_routes"], _ = json.Marshal(retired)
			}
			if len(kept) > 0 {
				step["predefined_routes"], _ = json.Marshal(kept)
			} else {
				delete(step, "predefined_routes")
			}
		}
		// Retire automatic contribution objectives for every executable Goal step,
		// including script post-processing. Configured access stays unchanged;
		// former objective-inferred grants are audit data only.
		if sc := byID[id]; sc != nil {
			var ac map[string]json.RawMessage
			if len(sc["agent_configs"]) > 0 {
				if err := json.Unmarshal(sc["agent_configs"], &ac); err != nil {
					return nil, err
				}
			}
			for _, pair := range [][3]string{{"learning_objective", "learnings_access", "learnings/_global"}, {"knowledgebase_contribution", "knowledgebase_access", "knowledgebase/notes"}} {
				var text, access string
				_ = json.Unmarshal(ac[pair[0]], &text)
				_ = json.Unmarshal(ac[pair[1]], &access)
				if strings.TrimSpace(text) == "" {
					continue
				}
				note[pair[0]] = ac[pair[0]]
				note[pair[0]+"_target"], _ = json.Marshal(pair[2])
				// Do not turn a legacy reflection's inferred permission into a
				// standing access setting. Keep configured access byte-for-byte;
				// the audit note records the old inferred grant for owner review.
				if access == "" {
					note[pair[0]+"_former_inferred_access"] = json.RawMessage(`"read-write"`)
				}
				delete(ac, pair[0])
			}
			if ac != nil {
				sc["agent_configs"], _ = json.Marshal(ac)
			}
		}
		if len(note) > 0 {
			note["version"] = json.RawMessage(`"1.0.50"`)
			step["goal_agent_migration"], _ = json.Marshal(note)
		}
		for _, key := range []string{"if_true_steps", "if_false_steps", "steps"} {
			if len(step[key]) == 0 {
				continue
			}
			var children []json.RawMessage
			if err := json.Unmarshal(step[key], &children); err != nil {
				return nil, err
			}
			for i := range children {
				var err error
				children[i], err = walk(children[i])
				if err != nil {
					return nil, err
				}
			}
			step[key], _ = json.Marshal(children)
		}
		return json.Marshal(step)
	}
	for _, key := range []string{"steps", "orphan_steps"} {
		if len(plan[key]) == 0 {
			continue
		}
		var steps []json.RawMessage
		if err := json.Unmarshal(plan[key], &steps); err != nil {
			return "", "", err
		}
		for i := range steps {
			var err error
			steps[i], err = walk(steps[i])
			if err != nil {
				return "", "", err
			}
		}
		plan[key], _ = json.Marshal(steps)
	}
	configs["steps"], _ = json.Marshal(configSteps)
	p, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return "", "", err
	}
	c, err := json.MarshalIndent(configs, "", "  ")
	return string(p), string(c), err
}

func createMigrateGoalAgentsExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, _ map[string]interface{}) (string, error) {
		planFileMutex.Lock()
		defer planFileMutex.Unlock()
		pp := normalizePathForWorkspaceAPI("planning/plan.json", workspacePath)
		cp := normalizePathForWorkspaceAPI("planning/step_config.json", workspacePath)
		before, err := readFile(ctx, pp)
		if err != nil {
			return "", err
		}
		cfg, readErr := readFile(ctx, cp)
		configAbsent := workspacepkg.IsFileNotFound(readErr)
		cfg, err = goalAgentConfigForMigration(cfg, readErr)
		if err != nil {
			return "", err
		}
		after, ac, err := migrateGoalAgentContent(before, cfg)
		if err != nil {
			return "", err
		}
		// Keep audit notes before clearing config. A failed second write is retryable:
		// the same notes are retained, and stamp validation still sees old objectives.
		if !sameAgentJSON([]byte(before), []byte(after)) {
			if err := writeFile(workspacepkg.WithSystemManagedWritePaths(ctx, pp), pp, after); err != nil {
				return "", err
			}
			logPlanChange(ctx, workspacePath, PlanChangelogEntry{Tool: "migrate_goal_agents", Reason: "Retire automatic reflection and LLM specialists; preserve original contributions/routes in each step's goal_agent_migration audit note. Review retired routes as a Crew step through Autopilot.", BeforeSnapshot: json.RawMessage(before), AfterSnapshot: json.RawMessage(after)}, readFile, withPlanMutationWriteAccess(workspacePath, writeFile), logger)
		}
		if !configAbsent && !sameAgentJSON([]byte(cfg), []byte(ac)) {
			if err := writeFile(workspacepkg.WithSystemManagedWritePaths(ctx, cp), cp, ac); err != nil {
				return "", err
			}
		}
		return `{"status":"migrated","next":"Read goal_agent_migration notes and the plan changelog. Preserve authored follow-up phases; move required contributions into explicit tasks. Raise an Autopilot issue for retired LLM routes; do not auto-create Crew. Then stamp 1.0.50."}`, nil
	}
}

// A configuration file is optional. Only a real not-found uses defaults;
// permission, transport and malformed-content errors must remain visible.
func goalAgentConfigForMigration(content string, readErr error) (string, error) {
	if workspacepkg.IsFileNotFound(readErr) {
		return `{"steps":[]}`, nil
	}
	return content, readErr
}

func validateGoalAgentContractStamp(plan, configs string) error {
	p, c, err := migrateGoalAgentContent(plan, configs)
	if err != nil {
		return err
	}
	if !sameAgentJSON([]byte(plan), []byte(p)) || !sameAgentJSON([]byte(configs), []byte(c)) {
		return fmt.Errorf("run migrate_goal_agents and review its preserved contribution/route notes before stamping %s", GoalAgentContractVersion)
	}
	return nil
}

func validateGoalScriptedRouteAuthoring(routes []PlanOrchestrationRoute, plans ...*PlanningResponse) error {
	for _, route := range routes {
		if route.SubAgentStep == nil && route.OrphanStepRef != "" {
			if len(plans) == 0 {
				continue
			} // early inline check; resolved against the plan before save
			route.SubAgentStep, _, _ = findStepByID(plans[0].OrphanSteps, route.OrphanStepRef)
		}
		if _, ok := route.SubAgentStep.(*ScriptedPlanStep); !ok {
			return fmt.Errorf("route %q needs a saved scripted Python helper; put LLM specialists in an explicit Crew step", route.RouteID)
		}
	}
	return nil
}

// ValidateGoalAgentAuthoring applies only to fresh definitions. Saved legacy
// plans still load normally so they can be inspected and explicitly migrated.
func ValidateGoalAgentAuthoring(plan *PlanningResponse) error {
	if plan == nil {
		return fmt.Errorf("a Goal plan is required")
	}
	for _, steps := range [][]PlanStepInterface{plan.Steps, plan.OrphanSteps} {
		for _, step := range steps {
			if agent, ok := step.(*AgentPlanStep); ok {
				if err := validateGoalScriptedRouteAuthoring(agent.PredefinedRoutes, plan); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
