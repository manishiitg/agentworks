package server

import (
	"fmt"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/cliruntime"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// linkedProjectCLIWorkingDir is the shared private provider projection for
// Crew, workflow/Relay and Vault chats. Callers select a server-owned mode;
// authorization and sandbox grants for the real project remain separate.
func linkedProjectCLIWorkingDir(folder, user, session, provider, mode string) (string, error) {
	project := codingAgentWorkspaceWorkingDir(folder)
	if !isCodingAgentProvider(provider, "") {
		return project, nil
	}
	stateRoot, err := workflowCLIStateRoot()
	if err != nil {
		return "", fmt.Errorf("cannot isolate project CLI: %w", err)
	}
	dir, err := cliruntime.PrepareLinkedProject(stateRoot, fsutil.WorkspaceDocsRoot(), user, project, session, provider, mode)
	if err != nil {
		return "", fmt.Errorf("cannot isolate project CLI: %w", err)
	}
	return dir, nil
}
