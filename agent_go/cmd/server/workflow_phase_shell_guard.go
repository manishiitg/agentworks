package server

import "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"

// configureWorkflowPhaseCLIShellGuard runs after the CLI working directory is
// selected. Ordinary turns keep the guard established from their current
// authenticated access; replacing it here would give Run turns write access
// to the workflow. Only an authorized external Builder operation needs its
// separate folder policy.
func configureWorkflowPhaseCLIShellGuard(sessionID, workspacePath, userID string, externalBuilder, readOnly bool) {
	if !externalBuilder || readOnly {
		return
	}
	readPaths, writePaths := externalBuilderFolderPaths(workspacePath)
	workspace.SetSessionFolderGuard(sessionID, readPaths, writePaths)
	workspace.SetSessionFolderGuardBlockedWritePaths(sessionID, []string{workspacePath + "/workflow.json", workspacePath + "/planning/"})
	protectOtherWorkflowBuilderChats(sessionID, workspacePath, userID)
}
