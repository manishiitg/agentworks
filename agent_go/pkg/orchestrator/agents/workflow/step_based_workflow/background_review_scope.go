package step_based_workflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GoalWorkPermissions mirror workflow.json pulse.autonomy. Each is true for
// "auto" (Goal Work does it itself) and false for "ask" (it prepares the work
// and creates a decision). Run defaults to auto; Outward and Change to ask.
type GoalWorkPermissions struct {
	// Run: run existing steps and routes, including what they normally do.
	Run bool
	// Outward: post, send or contact anyone beyond what existing steps do.
	Outward bool
	// Change: edit the plan, step settings and schedules. soul.md goals and
	// constraints always go to the user.
	Change bool
}

// PulseAutonomyPermissions reads workflow.json pulse.autonomy. Run is off only
// for an explicit "ask"; Outward and Change are on only for an explicit
// "auto". Missing or unreadable settings mean those defaults.
func PulseAutonomyPermissions(manifestJSON string) GoalWorkPermissions {
	perms := GoalWorkPermissions{Run: true}
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
// it must prepare for the user's approval, one sentence per permission. The
// server refuses the matching tools for an "ask" level during the Goal Work
// turn (cmd/server/pulse_autonomy_guard.go), so the text and the tools agree.
func goalWorkPermissionInstructions(perms GoalWorkPermissions) string {
	parts := []string{}
	if perms.Run {
		parts = append(parts, "Run permission: auto. Run existing workflow steps or routes yourself (execute_step, run_full_workflow) when that directly advances the goal or recovers work that did not happen; they do what they normally do, including their usual posts.")
	} else {
		parts = append(parts, "Run permission: ask. Do not run workflow steps, routes or Crews (the run tools refuse); prepare the work fully and create a decision request (create_human_input_request) asking the user to run it.")
	}
	if perms.Outward {
		parts = append(parts, "Outward permission: auto. You may post, send or contact people yourself with the workflow's own accounts and tools when it advances the goal, within soul.md limits and the workflow's own caps and dedupe records; verify each action landed and record it where the workflow records its own.")
	} else {
		parts = append(parts, "Outward permission: ask. Beyond what existing steps normally do, never post, send or contact anyone yourself (Slack posts and Google writes are refused; hold it the same way in the browser, shell and connected servers): prepare it fully and create a decision request for the user to approve.")
	}
	if perms.Change {
		parts = append(parts, "Change permission: auto. You may change how the workflow works yourself with the typed Builder tools (step prompts and items, step settings, schedules) when it advances the goal and every soul.md constraint still holds. soul.md goals and constraints are not yours to edit: challenge them through a decision. Never delete steps or schedules or replace the plan (those tools refuse); propose that through a decision.")
	} else {
		parts = append(parts, "Change permission: ask. Never edit the plan, steps, schedules or soul.md (the Builder edit tools refuse); propose them with a ready patch through a decision request.")
	}
	parts = append(parts, "Spending money or buying anything always goes to the user as a decision request.")
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

// GoalWorkEffectivePermissions are the levels Goal Work holds in this turn:
// the workflow's pulse.autonomy, with Run and Change withheld while a Plan
// Drift review is due (the plan is being reconciled; docs/design/pulse_goal_work.md).
func GoalWorkEffectivePermissions(manifestJSON string, planDriftDue bool) GoalWorkPermissions {
	perms := PulseAutonomyPermissions(manifestJSON)
	if planDriftDue {
		perms.Run = false
		perms.Change = false
	}
	return perms
}

// GoalWorkAutonomyInstructions is the permission text for a Goal Work turn.
// The server enforces the same levels on the tools for that turn (PLAT-697
// phase 2), so a refused call is the level working, not an error to retry.
func GoalWorkAutonomyInstructions(perms GoalWorkPermissions, planDriftDue bool) string {
	text := "Your permission levels for this turn (workflow.json pulse.autonomy). The tools hold them: a call refused for a level means prepare the work and create a decision request instead; do not retry it another way.\n"
	if planDriftDue {
		text += "Plan Drift is due, so Run and Change are held to ask for this turn whatever the workflow's setting: prepare and research, do not run steps or edit the workflow.\n"
	}
	return text + goalWorkPermissionInstructions(perms)
}
