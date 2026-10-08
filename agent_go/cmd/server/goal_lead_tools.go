package server

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

// pulseToolSurface is the Pulse conversation's tool gate name.
const pulseToolSurface = "pulse"

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
		"At auto, act on your own: decide, have the Builder chat do it now, check the result, and record it (record_pulse_goal_work); do not wait for or ask the owner, and do not raise a decision for it. At ask, still direct the Builder chat with what is best and why: it prepares the change, raises one decision for the owner (you attach your recommendation), and carries it out once the owner accepts. Spending money always goes to the owner. The Builder chat is held to these levels while it handles your message and refuses what they do not allow."
}

// builderPulseLevelsSection is the Builder chat's standing permission for
// work Pulse directs (owner, 2026-10-08: "depending on autonomy the builder
// should take actions on its own"). Without it the Builder asked the owner for
// a go-ahead the levels already gave. Empty when Pulse does not own the goal.
func builderPulseLevelsSection(ctx context.Context, workspacePath string) string {
	workspacePath = strings.Trim(strings.TrimSpace(workspacePath), "/")
	if workspacePath == "" || !workflowHasGoal(ctx, workspacePath) {
		return ""
	}
	perms, _ := goalWorkAutonomy(ctx, workspacePath)
	level := func(on bool) string {
		if on {
			return "auto"
		}
		return "ask"
	}
	return "## Pulse's permission levels\n\n" +
		"This workflow's Pulse owns its goal and directs you with what is best. The owner set these levels (workflow.json pulse.autonomy) as standing permission for work Pulse directs, whether Pulse messages you or you bring the owner's #pulse question to Pulse:\n" +
		"- Run: " + level(perms.Run) + ". Run the workflow's steps or routes.\n" +
		"- Outward: " + level(perms.Outward) + ". Post, publish, send or contact people.\n" +
		"- Change: " + level(perms.Change) + ". Change the plan, step settings or schedules.\n" +
		"At auto, do what Pulse directs now, without asking the owner, then report the result (to Pulse, and to the owner when they asked). Do not ask the owner for a go-ahead the level already gives, and do not end a reply to Pulse with a question for the owner. At ask, prepare it and raise one decision for the owner (create_human_input_request) with Pulse's recommendation, and carry it out once the owner accepts. Always the owner's: spending money, deleting steps or schedules, replacing the plan, editing soul.md, and re-enabling schedules the owner paused. Close the loop with Pulse: after acting on its direction, report what you did, what is still pending and when, and anything you did differently from its plan and why. When Pulse messaged you, your reply is that report; when you acted on the owner's #pulse message, send it to Pulse with ask_pulse in one message. Follow Pulse's intent, not just its words (\"extend the trial\" never shortens it); when unsure, say how you read it."
}

// configurePulseShellGuard: Pulse's shell reads the whole workflow and writes
// only its own folders (product.yaml pulse.write_paths).
func configurePulseShellGuard(sessionID, workspacePath string) {
	writes := []string{}
	for _, rel := range agentworksproduct.PulseWritePaths() {
		writes = append(writes, path.Join(workspacePath, rel)+"/")
	}
	workspace.SetSessionFolderGuard(sessionID, []string{workspacePath}, writes)
}

// pulseWriteDirs are Pulse's writable folders under the workflow's CLI
// directory, created so the sandbox can grant them.
func pulseWriteDirs(workflowDir string) []string {
	out := []string{}
	for _, rel := range agentworksproduct.PulseWritePaths() {
		dir := filepath.Join(workflowDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(dir, 0o755); err == nil {
			out = append(out, dir)
		}
	}
	return out
}
