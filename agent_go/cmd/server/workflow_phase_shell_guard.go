package server

import (
	"context"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

// configureWorkflowPhaseCLIShellGuard runs after the CLI working directory is
// selected. Ordinary turns keep the guard established from their current
// authenticated access; replacing it here would give Run turns write access
// to the workflow.
func configureWorkflowPhaseCLIShellGuard(sessionID, workspacePath, userID string, readOnly bool) {
	if m, ok, e := ReadWorkflowManifest(context.Background(), workspacePath); e == nil && ok && m.Team != nil {
		if isGoalLeadSessionID(sessionID) {
			configurePulseShellGuard(sessionID, workspacePath)
			protectOtherWorkflowBuilderChats(sessionID, workspacePath, userID)
			return
		}
		writes := []string{workspacePath + "/builder/conversation/users/" + sanitizeUserIDForPath(userID)}
		if !readOnly {
			writes = append(writes, workspacePath+"/runs")
		}
		workspace.SetSessionFolderGuard(sessionID, []string{workspacePath}, writes)
		protectOtherWorkflowBuilderChats(sessionID, workspacePath, userID)
	}
}
