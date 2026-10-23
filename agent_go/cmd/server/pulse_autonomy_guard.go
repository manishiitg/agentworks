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

// goalWorkNeverTools stay with the user whatever the autonomy: migrations, the
// contract version, secrets and access (owner, 2026-10-23: "not like spending
// money or security issues").
var goalWorkNeverTools = map[string]bool{
	"set_workflow_contract_version": true, "migrate_agent_code_items": true,
	"migrate_agent_steps": true, "migrate_goal_agents": true, "migrate_workflow_skills": true,
	"set_workflow_secret": true, "delete_workflow_secret": true, "manage_user_access": true, "manage_workflow_webhook": true,
}

// goalWorkNeverWords catch security tools by name so a new tool is held by
// default (reviewer, 2026-10-23): secrets, the vault, access and sharing,
// publishing, webhooks and laptop access stay with the owner. Reading tools
// (list_, get_, read_) stay available.
var goalWorkNeverWords = []string{"secret", "vault", "access", "share", "publish", "webhook", "laptop"}

func goalWorkNeverByName(tool string) bool {
	for _, read := range []string{"list_", "get_", "read_"} {
		if strings.HasPrefix(tool, read) {
			return false
		}
	}
	for _, word := range goalWorkNeverWords {
		if strings.Contains(tool, word) {
			return true
		}
	}
	return false
}

// goalWorkPlanTools replace the plan or delete steps and schedules. Autonomy
// "decide" lets Autopilot direct them; below it they stay with the user.
var goalWorkPlanTools = map[string]bool{
	"delete_plan_steps": true, "delete_schedule": true, "create_plan": true, "cleanup_orphan_step_configs": true,
	"convert_routing_branch_step_type": true,
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
		return fmt.Errorf("Autopilot is off for this workflow. Turn it on in the workflow's Autopilot settings to use it (%s)", tool)
	}

	switch {
	case goalWorkNeverTools[tool] || goalWorkNeverByName(tool):
		return fmt.Errorf("%s is not available to Autopilot work: migrations, secrets and access stay with the owner. Create a decision request (create_human_input_request) with the exact change instead", tool)
	case goalWorkPlanTools[tool]:
		if !perms.Decide {
			return fmt.Errorf("%s needs the owner: deleting steps or schedules and replacing the plan need autonomy \"decide\". Create a decision request (create_human_input_request) with the exact change instead", tool)
		}
	case goalWorkOutwardTools[tool]:
		if !perms.Outward {
			return fmt.Errorf("%s needs the owner's approval for an outward action. Prepare the action and ask through the existing decision flow", tool)
		}
	}
	return nil
}
