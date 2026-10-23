package server

import (
	"context"
	"encoding/json"
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

// pulseLevelsText states Pulse's permissions as what the Builder chat
// may do for it without the owner: Pulse itself changes and runs nothing.
func pulseLevelsText(perms stepworkflow.GoalWorkPermissions) string {
	return stepworkflow.PulsePermissionInstructions(perms) + " Autopilot judges and keeps records; Builder implements. Within these permissions, act without another owner confirmation, verify the result and record it. When the owner must decide, prepare one ready decision with evidence. The Builder receives the same server-held permissions with your message."
}

// builderPulseLevelsSection is the Builder chat's always-on Pulse section
// (owner, 2026-10-08): with Pulse on, who Pulse is and the levels it acts on
// (the owner's standing permission for work Pulse directs: without it the
// Builder asked the owner for a go-ahead the levels already gave); with Pulse
// on but no soul.md, that the goal is missing; with Pulse off, that the owner
// manages the workflow.
func builderPulseLevelsSection(ctx context.Context, workspacePath string) string {
	workspacePath = strings.Trim(strings.TrimSpace(workspacePath), "/")
	if workspacePath == "" {
		return ""
	}
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found || manifest == nil || manifest.Kind == "relay" {
		return ""
	}
	if !manifest.PulseEnabled() {
		return "## Autopilot\n\nAutopilot is off: you and the owner manage this workflow. It does not run, report or receive messages, including #pulse tags. When the owner wants an agent to own the goal, turn Autopilot on (update_workflow_config(pulse_enabled=true); the goal must be in soul/soul.md)."
	}
	if !workflowHasSoul(ctx, workspacePath) {
		return "## Autopilot\n\nAutopilot is on but waiting for the goal: soul/soul.md is missing. Help the owner write it (objective, the number that shows progress, a target and date); Autopilot starts once it exists."
	}
	perms, _ := pulsePermissionsForWorkflow(ctx, workspacePath)
	return "## Autopilot\n\n" +
		"This workflow's Autopilot owns its goal and directs you with what is best; you act. Ask it with ask_pulse (the owner's #pulse messages go to it in their words); read the working-with-pulse skill for how.\n\n" +
		stepworkflow.PulsePermissionInstructions(perms) + "\nIt is the owner's standing permission for work Autopilot directs, whether Autopilot messages you or you bring the owner's #pulse question to Autopilot.\n" +

		"Within these permissions, do what Autopilot directs now, without asking the owner, then report the result (to Autopilot, and to the owner when they asked). Do not ask the owner for a go-ahead these permissions already give, and do not end a reply to Autopilot with a question for the owner. When owner approval is required, prepare it and raise one decision for the owner (create_human_input_request) with Autopilot's recommendation, and carry it out once the owner accepts. Always the owner's: spending money, deleting steps or schedules, replacing the plan, editing soul.md, new kinds of outreach, and re-enabling schedules the owner paused. When an agent-issued pending proposal is obsolete after legitimately completed work, read it and withdraw_human_input_request with reason and evidence; withdrawal never grants approval or replaces a needed owner answer. Close the loop with Autopilot: after acting on its direction, report what you did, what is still pending and when, and anything you did differently from its plan and why. When Autopilot messaged you, explicitly send that report with send_message(inbox_id=<the supplied reply address>); your final chat answer is not forwarded. When you acted on the owner's #pulse message, send it to Autopilot with ask_pulse in one message. Read later explicit replies with read_agent_messages. Follow Autopilot's intent, not just its words (\"extend the trial\" never shortens it); when unsure, say how you read it."
}

// pulsePolicyKeySuffix is the Pulse part of a chat's policy key: Pulse's own
// system section for its conversation, or the Builder's copy of Pulse's levels
// and pace. A change relaunches the retained CLI on the same conversation.
// Every place that computes or compares the key must add it, or a follow-up
// looks like a policy change and cancels the running turn.
func pulsePolicyKeySuffix(sessionID, phaseID, workspacePath string) string {
	suffix := ""
	if m, found, e := ReadWorkflowManifest(context.Background(), workspacePath); e == nil && found && m.Team != nil {
		raw, _ := json.Marshal(m.Team)
		suffix = ":team-" + teamRevision(string(raw))
	}
	if isGoalLeadSessionID(sessionID) {
		if key := goalLeadSystemSectionKey(goalLeadSystemSection(context.Background(), workspacePath)); key != "" {
			return suffix + ":pulse-" + key
		}
		return suffix
	}
	if phaseID == "workflow-builder" {
		if key := goalLeadSystemSectionKey(builderPulseLevelsSection(context.Background(), workspacePath)); key != "" {
			return suffix + ":pulse-levels-" + key
		}
	}
	return suffix
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
