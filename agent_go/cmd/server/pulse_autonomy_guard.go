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
	if !perms.Run {
		return fmt.Errorf("Pulse is off for this workflow. Turn it on in the workflow's Pulse settings to use it (%s)", tool)
	}

	switch {
	case goalWorkNeverTools[tool]:
		return fmt.Errorf("%s is not available to Goal Work: deleting steps or schedules, replacing the plan and migrations stay with the user. Create a decision request (create_human_input_request) with the exact change instead", tool)
	case goalWorkOutwardTools[tool]:
		if !perms.Outward {
			return fmt.Errorf("%s needs the owner's approval for an outward action. Prepare the action and ask through the existing decision flow", tool)
		}
	}
	return nil
}

func pulseCheckRunIDForSession(sessionID string) string {
	goalWorkTurns.Lock()
	defer goalWorkTurns.Unlock()
	if hold := goalWorkTurns.bySession[sessionID]; hold != nil {
		return hold.checkRunID
	}
	return ""
}
