package server

import (
	"os"
	"path"
	"path/filepath"

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
		"At auto, ask the Builder chat and it acts; tell the owner after. At ask, prepare it and put one decision to the owner with your recommendation. Spending money always goes to the owner. The Builder chat is held to these levels while it handles your message."
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
