package server

import (
	"fmt"
	"strings"
	"sync"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Pulse on/off and outward permission are enforced at the existing tool
// dispatch boundary, including delegated Builder work.
//
// How it holds them:
//   - The scheduler marks the Pulse session as "in a Goal Work turn" with that
//     turn's permissions (beginGoalWorkTurn) and clears it when the turn ends. The
//     key is the server's own session ID, never anything the agent sends.
//   - Every direct tool call of that session passes bindToolExecutionContext,
//     which asks goalWorkToolRefusal and refuses with a message that says to
//     create a decision request instead.
//   - The tool catalog is never narrowed per turn: the same Pulse conversation
//     uses the full set, and a
//     catalog that changes between turns is how a registered tool became
//     undiscoverable before (docs/design/agent_tool_surface_single_source.md).
//     The tools stay visible; the call is refused.
//
// Workflow steps that an active Pulse starts keep doing what they
// normally do: their calls come through child MCP sessions registered to the
// run and are not held (see the callerOwnedBySession check at the call site).

var goalWorkTurns = struct {
	sync.Mutex
	bySession map[string]*goalWorkTurnHold
}{bySession: map[string]*goalWorkTurnHold{}}

type goalWorkTurnHold struct {
	perms      stepworkflow.GoalWorkPermissions
	checkRunID string
}

// beginGoalWorkTurn holds sessionID's tools to perms until the returned
// release runs. A later begin for the same session replaces the hold; an
// earlier release then leaves the newer hold in place.
func beginGoalWorkTurn(sessionID string, perms stepworkflow.GoalWorkPermissions, checkRunIDs ...string) (release func()) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return func() {}
	}
	hold := &goalWorkTurnHold{perms: perms}
	if len(checkRunIDs) > 0 {
		hold.checkRunID = checkRunIDs[0]
	}
	goalWorkTurns.Lock()
	goalWorkTurns.bySession[sessionID] = hold
	goalWorkTurns.Unlock()
	return func() {
		goalWorkTurns.Lock()
		if goalWorkTurns.bySession[sessionID] == hold {
			delete(goalWorkTurns.bySession, sessionID)
		}
		goalWorkTurns.Unlock()
	}
}

// goalWorkTurnPermissions reports the levels held on sessionID's tools, if a
// Goal Work turn is running in it.
func goalWorkTurnPermissions(sessionID string) (stepworkflow.GoalWorkPermissions, bool) {
	goalWorkTurns.Lock()
	defer goalWorkTurns.Unlock()
	hold, ok := goalWorkTurns.bySession[strings.TrimSpace(sessionID)]
	if !ok {
		return stepworkflow.GoalWorkPermissions{}, false
	}
	return hold.perms, true
}

// goalWorkRunTools start or steer workflow work: steps, routes, schedules
// fired now, Crews doing work.
var goalWorkRunTools = map[string]bool{
	"execute_step": true, "run_full_workflow": true, "debug_step": true, "send_step_message": true,
	"trigger_schedule": true, "ask_platform_crew": true,
	"call_function": true, "run_project_workflow": true,
}

// goalWorkMeasureTools set up measurement while Pulse is ON.
var goalWorkMeasureTools = map[string]bool{"configure_goal_metrics": true}

// goalWorkChangeTools edit existing steps, beyond the step
// edit tools that stepworkflow.ScheduleGuardedTool already names.
var goalWorkChangeTools = map[string]bool{
	"update_step_config": true, "restore_step_from_changelog": true, "edit_team_source": true,
}

// goalWorkReshapeTools change schedules and the plan's structure while Pulse is ON.
var goalWorkReshapeTools = map[string]bool{
	"create_schedule": true, "create_calendar_schedule": true, "update_schedule": true, "manage_workflow_webhook": true,
	"add_step": true, "add_scripted_step": true, "add_agent_step": true, "add_routing_step": true, "add_branch_step": true,
	"add_agent_route": true, "add_crew_step": true, "manage_step_route": true, "manage_group": true, "maintain_plan": true,
	"convert_routing_branch_step_type": true, "delete_agent_route": true,
	"update_workflow_config": true, "set_workflow_llm_config": true, "update_variable": true, "add_group": true,
	"update_group": true, "delete_group": true,
}

// goalWorkNeverTools stay with the user whatever the Pulse setting: deleting steps or
// schedules, replacing the plan, migrations and the contract version.
var goalWorkNeverTools = map[string]bool{
	"delete_plan_steps": true, "delete_schedule": true, "create_plan": true, "cleanup_orphan_step_configs": true,
	"change_step_type": true, "set_workflow_contract_version": true, "migrate_agent_code_items": true,
	"migrate_agent_steps": true, "migrate_goal_agents": true, "migrate_workflow_skills": true, "migrate_declared_execution_mode": true, "strip_declared_execution_mode": true,
}

// goalWorkOutwardTools post or send beyond the workflow by name alone. Tools
// whose effect depends on their arguments (slack, google_workspace_cli,
// workflow summary delivery with other recipients) are held inside the tool through
// common.WithOutwardHeld.
var goalWorkOutwardTools = map[string]bool{"send_slack_message": true, "send_email": true}

// goalWorkToolRefusal returns the refusal for a tool call made in a Goal Work
// turn held to perms, or nil when the call may run.
func goalWorkToolRefusal(tool string, perms stepworkflow.GoalWorkPermissions) error {
	tool = strings.TrimSpace(tool)
	if !perms.Run && !pulseReportReadTools[tool] {
		return fmt.Errorf("%s is unavailable while Pulse is off: save suggestions in the weekly report without edits or action requests. The owner can turn Pulse on to act", tool)
	}
	switch {
	case goalWorkNeverTools[tool]:
		return fmt.Errorf("%s is not available to Goal Work: deleting steps or schedules, replacing the plan and migrations stay with the user. Create a decision request (create_human_input_request) with the exact change instead", tool)
	case goalWorkRunTools[tool] || goalWorkMeasureTools[tool] || goalWorkReshapeTools[tool] || goalWorkChangeTools[tool] || stepworkflow.ScheduleGuardedTool(tool):
		if !perms.Run {
			return fmt.Errorf("%s is unavailable while Pulse is off: this weekly report can suggest changes but cannot run or edit work. The owner can turn Pulse on to act on the suggestions", tool)
		}
	case goalWorkOutwardTools[tool]:
		if !perms.Run {
			return fmt.Errorf("%s is unavailable while Pulse is off: this weekly report can suggest changes but cannot send action requests. The owner can turn Pulse on to act", tool)
		}
		if !perms.Outward {
			return fmt.Errorf("%s needs the owner's approval for an outward action. Prepare the action and ask through the existing decision flow", tool)
		}
	}
	return nil
}

// OFF has only reads plus its one report write. New tools are never assumed
// to be observations merely because their name is absent from a denylist.
var pulseReportReadTools = map[string]bool{
	"record_pulse_goal_check": true,
	"get_pulse_state":         true, "get_goal_metrics": true, "query_workflow_db": true, "query_workflow_costs": true,
	"get_cost_summary": true, "get_step_prompts": true, "get_plan_prompt_health": true, "get_workflow_config": true,
	"get_llm_config": true, "list_executions": true, "get_schedule_runs": true, "list_schedules": true, "query_step": true,
	"get_notification_history": true, "get_human_input_request": true, "list_skills": true, "search_skills": true,
	"get_workflow_command_guidance": true, "get_file_link": true, "get_report_link": true, "read_image": true,
	"read_crew_calls": true, "search_platform": true, "search_playbooks": true, "brain_browse": true, "brain_read": true,
	"list_accessible_workflows": true, "get_contract_upgrades": true, "read_agent_messages": true, "list_message_agents": true,
	"get_function_call": true, "read_skill": true, "read_workspace_file": true, "list_workspace_files": true, "search_workspace_files": true,
	"get_api_spec": true, "get_prompt": true, "get_resource": true,
}

func pulseCheckRunIDForSession(sessionID string) string {
	goalWorkTurns.Lock()
	defer goalWorkTurns.Unlock()
	if hold := goalWorkTurns.bySession[sessionID]; hold != nil {
		return hold.checkRunID
	}
	return ""
}
