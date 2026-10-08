package server

import stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"

// pulseToolSurface is the Pulse conversation's tool gate name.
const pulseToolSurface = "pulse"

// pulseTools is everything the Pulse conversation gets (owner, 2026-10-08:
// "pulse can itself just have access to read only stuff; it can ask the
// Builder to do all these things"). Pulse reads the workflow, keeps its own
// records (goal checks, memory, recommendations, focus areas, decisions,
// notifications) and talks to the Builder chat. Anything that changes or runs
// the workflow, its schedules, secrets, models or skills is done by the
// Builder chat through ask_builder, within Pulse's permission levels, even
// with full autonomy. execute_shell_command stays: a coding CLI reaches its
// tools through it.
var pulseTools = []string{
	// Read the workflow.
	"get_pulse_state", "get_goal_metrics", "query_workflow_db", "query_workflow_costs", "get_cost_summary",
	"get_step_prompts", "get_plan_prompt_health", "get_workflow_config", "get_llm_config", "list_executions",
	"get_schedule_runs", "list_schedules", "query_step", "get_notification_history", "get_human_input_request",
	"list_skills", "search_skills", "get_workflow_command_guidance", "get_file_link", "get_report_link",
	"read_image", "read_crew_calls", "search_platform", "search_playbooks", "brain_browse", "brain_access",
	"list_accessible_workflows", "get_contract_upgrades", "execute_shell_command",
	// Pulse's own records.
	"record_pulse_goal_check", "record_pulse_goal_memory", "record_pulse_recommendation", "record_pulse_decision_outcome",
	"record_pulse_focus_area", "record_pulse_qa_request", "record_pulse_goal_work", "record_pulse_result",
	"record_pulse_worklist", "record_pulse_next_run", "record_pulse_fast_request", "record_pulse_finding",
	"merge_pulse_issues", "resolve_run_concern", "record_goal_observations",
	"create_human_input_request", "dismiss_duplicate_human_input_request", "notify_user",
	// Talk to the Builder chat.
	"ask_builder",
}

// pulseLevelsText states Pulse's permission levels as what the Builder chat
// may do for it without the owner: Pulse itself changes and runs nothing.
func pulseLevelsText(perms stepworkflow.GoalWorkPermissions) string {
	level := func(on bool) string {
		if on {
			return "auto"
		}
		return "ask"
	}
	return "Your permission levels (workflow.json pulse.autonomy) say what the Builder chat may do when you ask it, without the owner:\n" +
		"- Run: " + level(perms.Run) + ". Run the workflow's steps or routes.\n" +
		"- Outward: " + level(perms.Outward) + ". Post, send or contact people beyond what the steps normally do.\n" +
		"- Change: " + level(perms.Change) + ". Change the plan, step settings or schedules (never delete steps or schedules, replace the plan, or edit soul.md).\n" +
		"At auto, ask the Builder chat and it acts; tell the owner after. At ask, prepare it and put one decision to the owner with your recommendation. Spending money always goes to the owner. The Builder chat is held to these levels while it handles your message."
}
