package server

import (
	"fmt"
	"strings"
	"sync"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Goal Work's autonomy levels (workflow.json pulse.autonomy: run, outward,
// change) are enforced on the tools, not only stated in its prompt (PLAT-697
// phase 2). Until then they were prose: PLAT-452 removed the background agent
// whose filtered tool list held them.
//
// How it holds them:
//   - The scheduler marks the Pulse session as "in a Goal Work turn" with that
//     turn's levels (beginGoalWorkTurn) and clears it when the turn ends. The
//     key is the server's own session ID, never anything the agent sends.
//   - Every direct tool call of that session passes bindToolExecutionContext,
//     which asks goalWorkToolRefusal and refuses with a message that says to
//     create a decision request instead.
//   - The tool catalog is never narrowed per turn: the same Pulse conversation
//     runs Gate, the reviews and Finalize with the full set, and a
//     catalog that changes between turns is how a registered tool became
//     undiscoverable before (docs/design/agent_tool_surface_single_source.md).
//     The tools stay visible; the call is refused.
//
// Workflow steps that Goal Work starts with Run auto keep doing what they
// normally do: their calls come through child MCP sessions registered to the
// run and are not held (see the callerOwnedBySession check at the call site).

var goalWorkTurns = struct {
	sync.Mutex
	bySession map[string]*goalWorkTurnHold
}{bySession: map[string]*goalWorkTurnHold{}}

type goalWorkTurnHold struct {
	perms stepworkflow.GoalWorkPermissions
}

// beginGoalWorkTurn holds sessionID's tools to perms until the returned
// release runs. A later begin for the same session replaces the hold; an
// earlier release then leaves the newer hold in place.
func beginGoalWorkTurn(sessionID string, perms stepworkflow.GoalWorkPermissions) (release func()) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return func() {}
	}
	hold := &goalWorkTurnHold{perms: perms}
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

// goalWorkMeasureTools set up measurement: allowed from the Measure level.
var goalWorkMeasureTools = map[string]bool{"configure_goal_metrics": true}

// goalWorkChangeTools edit existing steps (Fix and Tune), beyond the step
// edit tools that stepworkflow.ScheduleGuardedTool already names.
var goalWorkChangeTools = map[string]bool{
	"update_step_config": true, "restore_step_from_changelog": true,
}

// goalWorkReshapeTools change schedules and the plan's structure: only at the
// Reshape level (owner, 2026-10-08 autonomy ladder).
var goalWorkReshapeTools = map[string]bool{
	"create_schedule": true, "create_calendar_schedule": true, "update_schedule": true, "manage_workflow_webhook": true,
	"add_step": true, "add_scripted_step": true, "add_agent_step": true, "add_routing_step": true, "add_branch_step": true,
	"add_human_input_step": true, "add_agent_route": true, "add_crew_step": true, "manage_step_route": true, "manage_group": true, "maintain_plan": true,
	"convert_routing_branch_step_type": true, "delete_agent_route": true,
	"update_workflow_config": true, "set_workflow_llm_config": true, "update_variable": true, "add_group": true,
	"update_group": true, "delete_group": true,
}

// goalWorkNeverTools stay with the user whatever the levels: deleting steps or
// schedules, replacing the plan, migrations and the contract version.
var goalWorkNeverTools = map[string]bool{
	"delete_plan_steps": true, "delete_schedule": true, "create_plan": true, "cleanup_orphan_step_configs": true,
	"change_step_type": true, "set_workflow_contract_version": true, "migrate_agent_code_items": true,
	"migrate_orchestrator_step_type": true, "migrate_declared_execution_mode": true, "strip_declared_execution_mode": true,
}

// goalWorkOutwardTools post or send beyond the workflow by name alone. Tools
// whose effect depends on their arguments (slack, google_workspace_cli,
// notify_user with other recipients) are held inside the tool through
// common.WithOutwardHeld.
var goalWorkOutwardTools = map[string]bool{"send_slack_message": true}

// goalWorkToolRefusal returns the refusal for a tool call made in a Goal Work
// turn held to perms, or nil when the call may run.
func goalWorkToolRefusal(tool string, perms stepworkflow.GoalWorkPermissions) error {
	tool = strings.TrimSpace(tool)
	switch {
	case goalWorkNeverTools[tool]:
		return fmt.Errorf("%s is not available to Goal Work: deleting steps or schedules, replacing the plan and migrations stay with the user. Create a decision request (create_human_input_request) with the exact change instead", tool)
	case goalWorkRunTools[tool] || goalWorkMeasureTools[tool]:
		if !perms.Run {
			return fmt.Errorf("%s refused: running steps needs the Measure autonomy level (now level %d, pulse.autonomy.level). Do not run it another way; prepare the work and have it put to the owner as one decision", tool, perms.Level)
		}
	case goalWorkReshapeTools[tool]:
		if !perms.Reshape {
			return fmt.Errorf("%s refused: schedules and the plan's structure need the Reshape autonomy level (now level %d, pulse.autonomy.level). Do not change it another way; have it put to the owner as one decision with the ready patch", tool, perms.Level)
		}
	case goalWorkChangeTools[tool] || stepworkflow.ScheduleGuardedTool(tool):
		if !perms.Change {
			return fmt.Errorf("%s refused: editing steps needs the Fix autonomy level (now level %d, pulse.autonomy.level). Do not edit the workflow another way; have it put to the owner as one decision with the ready patch", tool, perms.Level)
		}
	case goalWorkOutwardTools[tool]:
		if !perms.Outward {
			return fmt.Errorf("%s refused: posting needs the Publish autonomy level (now level %d, pulse.autonomy.level). Prepare the message and have it put to the owner as one decision", tool, perms.Level)
		}
	}
	return nil
}
