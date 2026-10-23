package step_based_workflow

import (
	"encoding/json"
	"fmt"
)

// GoalWorkPermissions are the server-held action permissions for the current
// Pulse turn. The workflow exposes on/off and one autonomy setting stored as
// outward_actions (ask, allow or decide); the tool groups below are internal
// execution checks, not configurable levels.
type GoalWorkPermissions struct {
	Run     bool
	Outward bool
	Change  bool
	Reshape bool
	// Decide lets Autopilot decide plan changes itself (autonomy "decide");
	// spending money and security stay the owner's.
	Decide bool
}

// PulsePermissions consumes the canonical manifest after saved-setting
// normalization. OFF starts no Pulse turn; ON can run and improve within
// existing access rules. Outward actions remain a separate owner choice.
func PulsePermissions(manifestJSON string) GoalWorkPermissions {
	var manifest struct {
		Pulse *struct {
			Enabled        bool   `json:"enabled"`
			OutwardActions string `json:"outward_actions"`
		} `json:"pulse"`
	}
	if json.Unmarshal([]byte(manifestJSON), &manifest) != nil || manifest.Pulse == nil || !manifest.Pulse.Enabled {
		return GoalWorkPermissions{}
	}
	level := manifest.Pulse.OutwardActions
	return GoalWorkPermissions{Run: true, Change: true, Reshape: true, Outward: level == "allow" || level == "decide", Decide: level == "decide"}
}

// PulseOwnerOnlyText is the one list of what stays the owner's, by autonomy.
func PulseOwnerOnlyText(perms GoalWorkPermissions) string {
	if perms.Decide {
		return "Always the owner's: spending money, anything about security (secrets, logins and accounts, access, sharing and permissions, deleting data), editing soul.md, new kinds of outreach, and re-enabling schedules the owner paused."
	}
	return "Always the owner's: spending money, deleting steps or schedules, replacing the plan, editing soul.md, new kinds of outreach, and re-enabling schedules the owner paused."
}

// PulsePermissionInstructions is shared by Pulse and Builder. It replaces the
// retired ladder instead of layering new permission advice over it.
func PulsePermissionInstructions(perms GoalWorkPermissions) string {
	if !perms.Run {
		return "Autopilot is off for this workflow. No checks, reports or messages run. Turn it on in the workflow's Autopilot settings to use it."
	}
	outward := "Ask the owner before new outward actions; prepare the ready action for their decision."
	if perms.Outward {
		outward = "Outward actions are allowed within the workflow's existing access, selected accounts and operational limits."
	}
	decide := ""
	if perms.Decide {
		decide = " Decide plan changes yourself: replace or restructure the plan and delete steps or schedules when the evidence supports it, record why and how to undo it, and do not raise a decision for them. Withdraw earlier decisions your own work made obsolete."
	}
	return "Autopilot is on. Pursue one bounded improvement change set in this cycle through Builder, including runs, step and schedule improvements within existing access. Check plan consistency in the same turn; never hold ordinary execution for an AI review. The unreadable-input guard still applies. " + outward + decide + " " + PulseOwnerOnlyText(perms)
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
	case "get_workflow_team", "read_team_source", "execute_shell_command", "read_workspace_file", "list_workspace_files", "search_workspace_files", "read_skill", "get_api_spec", "get_prompt", "get_resource",
		"query_workflow_db", "query_workflow_costs", "get_step_prompts", "get_plan_prompt_health", "get_workflow_config", "get_llm_config", "get_cost_summary",
		"list_executions", "get_sub_agent_conversation", "get_route_description", "get_goal_metrics":
		return true
	}
	return false
}
