package step_based_workflow

import (
	"encoding/json"
	"fmt"
)

// GoalWorkPermissions are the server-held action permissions for the current
// Pulse turn. The workflow exposes only on/off and outward_actions; the tool
// groups below are internal execution checks, not configurable levels.
type GoalWorkPermissions struct {
	Run     bool
	Outward bool
	Change  bool
	Reshape bool
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
	return GoalWorkPermissions{Run: true, Change: true, Reshape: true, Outward: manifest.Pulse.OutwardActions == "allow"}
}

// PulsePermissionInstructions is shared by Pulse and Builder. It replaces the
// retired ladder instead of layering new permission advice over it.
func PulsePermissionInstructions(perms GoalWorkPermissions) string {
	if !perms.Run {
		return "Pulse is off for this workflow. No checks, reports or messages run. Turn it on in the workflow's Pulse settings to use it."
	}
	outward := "Ask the owner before new outward actions; prepare the ready action for their decision."
	if perms.Outward {
		outward = "Outward actions are allowed within the workflow's existing access, selected accounts and operational limits."
	}
	return "Pulse is on. Pursue one bounded improvement change set in this cycle through Builder, including runs, step and schedule improvements within existing access. Check plan consistency in the same turn; never hold ordinary execution for an AI review. The unreadable-input guard still applies. " + outward + " Spending money, deleting steps or schedules, replacing the plan, editing soul.md, new kinds of outreach and re-enabling owner-paused schedules remain the owner's decisions."
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
		"list_executions", "get_sub_agent_conversation", "get_route_description", "get_goal_metrics", "get_pulse_state":
		return true
	}
	return false
}
