package step_based_workflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

// goalWorkPermissions mirror workflow.json pulse.autonomy. Each is true for
// "auto" (Goal Work does it itself) and false for "ask" (it prepares the work
// and creates a decision). Run defaults to auto; Outward and Change to ask.
type goalWorkPermissions struct {
	// Run: run existing steps and routes, including what they normally do.
	Run bool
	// Outward: post, send or contact anyone beyond what existing steps do.
	Outward bool
	// Change: edit the plan, step settings and schedules. soul.md goals and
	// constraints always go to the user.
	Change bool
}

// pulseAutonomyPermissions reads workflow.json pulse.autonomy. Run is off only
// for an explicit "ask"; Outward and Change are on only for an explicit
// "auto". Missing or unreadable settings mean those defaults.
func pulseAutonomyPermissions(manifestJSON string) goalWorkPermissions {
	perms := goalWorkPermissions{Run: true}
	var manifest struct {
		Pulse *struct {
			Autonomy *struct {
				Run     string `json:"run"`
				Outward string `json:"outward"`
				Change  string `json:"change"`
			} `json:"autonomy"`
		} `json:"pulse"`
	}
	if err := json.Unmarshal([]byte(manifestJSON), &manifest); err != nil || manifest.Pulse == nil || manifest.Pulse.Autonomy == nil {
		return perms
	}
	level := func(v string) string { return strings.ToLower(strings.TrimSpace(v)) }
	a := manifest.Pulse.Autonomy
	perms.Run = level(a.Run) != "ask"
	perms.Outward = level(a.Outward) == "auto"
	perms.Change = level(a.Change) == "auto"
	return perms
}

// goalWorkPermissionInstructions tells Goal Work what it may do itself and what
// it must prepare for the user's approval, one sentence per permission.
func goalWorkPermissionInstructions(perms goalWorkPermissions) string {
	parts := []string{}
	if perms.Run {
		parts = append(parts, "Run permission: auto. Run existing workflow steps or routes yourself (execute_step, run_full_workflow) when that directly advances the goal or recovers work that did not happen; they do what they normally do, including their usual posts.")
	} else {
		parts = append(parts, "Run permission: ask. Do not run workflow steps; prepare the work fully and create a decision asking the user to run it.")
	}
	if perms.Outward {
		parts = append(parts, "Outward permission: auto. You may post, send or contact people yourself with the workflow's own accounts and tools when it advances the goal, within soul.md limits and the workflow's own caps and dedupe records; verify each action landed and record it where the workflow records its own.")
	} else {
		parts = append(parts, "Outward permission: ask. Beyond what existing steps normally do, never post, send or contact anyone yourself: prepare it fully and create a decision for the user to approve.")
	}
	if perms.Change {
		parts = append(parts, "Change permission: auto. You may change how the workflow works yourself with the typed Builder tools (step prompts and items, step settings, schedules) when it advances the goal and every soul.md constraint still holds. soul.md goals and constraints are not yours to edit: challenge them through a decision. Never delete steps or schedules; propose that through a decision.")
	} else {
		parts = append(parts, "Change permission: ask. Never edit the plan, steps, schedules or soul.md; propose them with a ready patch through a decision.")
	}
	return strings.Join(parts, " ")
}

func parseBackgroundReadOnlyAccess(args map[string]interface{}, agentType string) (bool, error) {
	value, present := args["access_mode"]
	if !present {
		return false, nil
	}
	mode, ok := value.(string)
	if !ok || (mode != "read_write" && mode != "read_only") {
		return false, fmt.Errorf("access_mode must be read_write or read_only")
	}
	if mode == "read_only" && agentType != "executor" {
		return false, fmt.Errorf("read_only requires an executor")
	}
	return mode == "read_only", nil
}
func readOnlyBackgroundToolAllowed(name string) bool {
	switch name {
	case "execute_shell_command", "read_workspace_file", "list_workspace_files", "search_workspace_files", "read_skill", "get_api_spec", "get_prompt", "get_resource",
		"query_workflow_db", "query_workflow_costs", "get_step_prompts", "get_plan_prompt_health", "get_workflow_config", "get_llm_config", "get_cost_summary",
		"list_executions", "get_sub_agent_conversation", "get_route_description", "get_goal_metrics", "get_pulse_state":
		return true
	}
	return false
}

// GoalWorkAutonomyInstructions turns the workflow's pulse.autonomy levels into
// the permission text Goal Work must follow. Goal Work now runs in the Pulse
// conversation, whose tools are not filtered per module, so the limits are held
// by the agent and the text says so (PLAT-452).
func GoalWorkAutonomyInstructions(manifestJSON string) string {
	return "These permission levels are not enforced by the tools in this turn; hold them yourself:\n" +
		goalWorkPermissionInstructions(pulseAutonomyPermissions(manifestJSON))
}
